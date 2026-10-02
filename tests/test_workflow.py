from concurrent.futures import ThreadPoolExecutor

import pytest
from sqlalchemy import update

from jobadder_autocoder.audit.review import ReviewService
from jobadder_autocoder.connectors.demo import DemoConnector
from jobadder_autocoder.contracts.errors import Conflict
from jobadder_autocoder.contracts.models import Approval, Candidate, Mutation
from jobadder_autocoder.jobs.worker import Worker
from jobadder_autocoder.policy.country import PolicyError
from jobadder_autocoder.storage.database import Storage
from jobadder_autocoder.storage.models import CandidateRow
from tests.conftest import make_run, proposal_for


def test_preview_is_read_only_and_reports_real_outcomes(storage: Storage, worker: Worker) -> None:
    with storage.transaction() as repository:
        before = repository.page_candidates(0, 9999, 100).candidates
    run = make_run(storage, worker, "preview")
    assert (run.total, run.processed, run.missing, run.proposed, run.existing, run.not_found) == (
        10,
        10,
        8,
        6,
        2,
        2,
    )
    with storage.transaction() as repository:
        assert repository.page_candidates(0, 9999, 100).candidates == before
        assert all(s.state == "preview" for s in repository.suggestions(run.id))
        assert repository.writes(run.id) == []
    with pytest.raises(Conflict):
        ReviewService(storage).approve(proposal_for(storage, run.id, 1001), Approval())


def test_approval_changes_only_country_and_is_audited_once(
    storage: Storage, worker: Worker
) -> None:
    run = make_run(storage, worker)
    suggestion_id = proposal_for(storage, run.id, 1001)
    with storage.transaction() as repository:
        before = repository.get_candidate(1001)
    service = ReviewService(storage)
    result = service.approve(suggestion_id, Approval())
    assert result["state"] == "applied"
    with storage.transaction() as repository:
        after = repository.get_candidate(1001)
        assert after.country == "AU"
        assert before.model_dump(exclude={"country", "version"}) == after.model_dump(
            exclude={"country", "version"}
        )
        assert len(repository.writes(run.id)) == 1
        event = next(e for e in repository.audit_events() if e["action"] == "country_approved")
        assert event["details"]["before"] is None
        assert event["details"]["after"] == "AU"
    with pytest.raises(Conflict):
        service.approve(suggestion_id, Approval())


def test_a_field_filled_after_scan_is_skipped(storage: Storage, worker: Worker) -> None:
    run = make_run(storage, worker)
    with storage.transaction() as repository:
        repository.edit_demo_country(1001, "SG")
    result = ReviewService(storage).approve(proposal_for(storage, run.id, 1001), Approval())
    assert result["state"] == "skipped"
    with storage.transaction() as repository:
        assert repository.get_candidate(1001).country == "SG"
        assert repository.writes(run.id) == []


def test_revision_guard_rejects_a_stale_candidate(storage: Storage) -> None:
    with storage.transaction() as repository:
        before = repository.get_candidate(1001)
    with storage.transaction() as repository:
        repository.edit_demo_country(1001, "NZ")
    with storage.transaction() as repository:
        result = DemoConnector(repository).fill_country(before, "AU")
        assert not result.applied
        assert result.candidate.country == "NZ"


def test_concurrent_approvals_have_one_effect(storage: Storage, worker: Worker) -> None:
    run = make_run(storage, worker)
    suggestion_id = proposal_for(storage, run.id, 1001)

    def approve() -> str:
        try:
            return str(ReviewService(storage).approve(suggestion_id, Approval())["state"])
        except Conflict:
            return "conflict"

    with ThreadPoolExecutor(max_workers=2) as pool:
        outcomes = list(pool.map(lambda _: approve(), range(2)))
    assert sorted(outcomes) == ["applied", "conflict"]
    with storage.transaction() as repository:
        assert len(repository.writes(run.id)) == 1


def test_conflict_resolution_requires_a_correction_note(storage: Storage, worker: Worker) -> None:
    run = make_run(storage, worker)
    suggestion_id = proposal_for(storage, run.id, 1006)
    service = ReviewService(storage)
    with pytest.raises(PolicyError, match="Explain"):
        service.approve(suggestion_id, Approval(value="NZ"))
    assert (
        service.approve(suggestion_id, Approval(value="NZ", reason="Confirmed current residence."))[
            "state"
        ]
        == "applied"
    )


def test_changed_source_is_not_approved(storage: Storage, worker: Worker) -> None:
    run = make_run(storage, worker)
    with storage.transaction() as repository:
        repository.session.execute(
            update(CandidateRow)
            .where(CandidateRow.id == 1001)
            .values(address_country="New Zealand", version=2)
        )
    with pytest.raises(PolicyError, match="does not occur"):
        ReviewService(storage).approve(proposal_for(storage, run.id, 1001), Approval())
    with storage.transaction() as repository:
        assert repository.get_candidate(1001).country is None
        assert repository.get_suggestion(proposal_for(storage, run.id, 1001)).state == "pending"


def test_unexpected_adapter_changes_roll_back_the_whole_approval(
    storage: Storage, worker: Worker
) -> None:
    class BrokenConnector(DemoConnector):
        def fill_country(self, candidate: Candidate, value: str) -> Mutation:
            mutation = super().fill_country(candidate, value)
            self.repository.session.execute(
                update(CandidateRow)
                .where(CandidateRow.id == candidate.id)
                .values(notes="Unexpected change")
            )
            return Mutation(applied=mutation.applied, candidate=self.get(candidate.id))

    run = make_run(storage, worker)
    with pytest.raises(PolicyError, match="rolled back"):
        ReviewService(storage, BrokenConnector).approve(
            proposal_for(storage, run.id, 1001), Approval()
        )
    with storage.transaction() as repository:
        assert repository.get_candidate(1001).country is None
        assert repository.get_candidate(1001).notes == ""
        assert repository.writes(run.id) == []


def test_undo_restores_exact_original_empty_value(storage: Storage, worker: Worker) -> None:
    run = make_run(storage, worker)
    service = ReviewService(storage)
    result = service.approve(proposal_for(storage, run.id, 1003), Approval())
    assert service.undo(result["writeback_id"])["state"] == "undone"
    with storage.transaction() as repository:
        assert repository.get_candidate(1003).country == ""
    with pytest.raises(Conflict):
        service.undo(result["writeback_id"])


def test_undo_preserves_subsequent_edits(storage: Storage, worker: Worker) -> None:
    run = make_run(storage, worker)
    service = ReviewService(storage)
    result = service.approve(proposal_for(storage, run.id, 1001), Approval())
    with storage.transaction() as repository:
        repository.edit_demo_country(1001, "NZ")
    assert service.undo(result["writeback_id"])["state"] == "undo_skipped"
    with storage.transaction() as repository:
        assert repository.get_candidate(1001).country == "NZ"


def test_concurrent_undo_has_one_effect(storage: Storage, worker: Worker) -> None:
    run = make_run(storage, worker)
    service = ReviewService(storage)
    write_id = service.approve(proposal_for(storage, run.id, 1001), Approval())["writeback_id"]

    def undo() -> str:
        try:
            return service.undo(write_id)["state"]
        except Conflict:
            return "conflict"

    with ThreadPoolExecutor(max_workers=2) as pool:
        outcomes = list(pool.map(lambda _: undo(), range(2)))
    assert sorted(outcomes) == ["conflict", "undone"]
    with storage.transaction() as repository:
        assert repository.get_write(write_id).state == "undone"
        assert repository.get_candidate(1001).country is None
        assert len([e for e in repository.audit_events() if e["action"] == "country_undo"]) == 1


def test_rejection_is_audited_without_mutation(storage: Storage, worker: Worker) -> None:
    run = make_run(storage, worker)
    suggestion_id = proposal_for(storage, run.id, 1001)
    ReviewService(storage).reject(suggestion_id)
    with storage.transaction() as repository:
        assert repository.get_candidate(1001).country is None
        assert repository.get_suggestion(suggestion_id).state == "rejected"
        assert any(e["action"] == "country_rejected" for e in repository.audit_events())
