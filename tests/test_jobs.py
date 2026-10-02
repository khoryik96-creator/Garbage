from concurrent.futures import ThreadPoolExecutor
from dataclasses import replace

import pytest

from jobadder_autocoder.contracts.errors import LostLease
from jobadder_autocoder.contracts.models import Candidate, Extraction, RunRequest
from jobadder_autocoder.jobs.worker import Worker
from jobadder_autocoder.pipeline.country import RuleCountryExtractor
from jobadder_autocoder.settings import Settings
from jobadder_autocoder.storage.database import Storage


def test_checkpoint_survives_a_new_storage_instance(storage: Storage, settings: Settings) -> None:
    with storage.transaction() as repository:
        run = repository.new_run(RunRequest())
    assert Worker(storage, settings).process_one()
    with storage.transaction() as repository:
        assert repository.get_run(run.id).processed == 3
    storage.close()
    reopened = Storage(settings.database_url)
    try:
        reopened.migrate()
        worker = Worker(reopened, settings)
        for _ in range(10):
            if not worker.process_one():
                break
        with reopened.transaction() as repository:
            finished = repository.get_run(run.id)
            proposals = repository.suggestions(run.id)
        assert finished.state == "completed" and finished.processed == 10
        assert len(proposals) == 6
        assert len({s.candidate_id for s in proposals}) == 6
    finally:
        reopened.close()


def test_pause_and_resume_keep_progress(storage: Storage, settings: Settings) -> None:
    worker = Worker(storage, settings)
    with storage.transaction() as repository:
        run = repository.new_run(RunRequest())
    worker.process_one()
    with storage.transaction() as repository:
        repository.change_run_state(run.id, "pause")
    assert not worker.process_one()
    with storage.transaction() as repository:
        assert repository.get_run(run.id).processed == 3
        repository.change_run_state(run.id, "resume")
    for _ in range(10):
        if not worker.process_one():
            break
    with storage.transaction() as repository:
        assert repository.get_run(run.id).state == "completed"


def test_cancelled_run_has_no_further_work(storage: Storage, settings: Settings) -> None:
    with storage.transaction() as repository:
        run = repository.new_run(RunRequest())
        repository.change_run_state(run.id, "cancel")
    assert not Worker(storage, settings).process_one()


def test_expired_lease_is_reclaimed_and_old_owner_is_fenced(
    storage: Storage, settings: Settings
) -> None:
    with storage.transaction() as repository:
        run = repository.new_run(RunRequest())
        old = repository.claim_job(100, 60)
    assert old
    with storage.transaction() as repository:
        assert repository.claim_job(159, 60) is None
        new = repository.claim_job(161, 60)
    assert new and new.token != old.token
    worker = Worker(storage, settings, clock=lambda: 161)
    with pytest.raises(LostLease):
        worker.process_claim(old)
    worker.process_claim(new)
    with storage.transaction() as repository:
        assert repository.get_run(run.id).processed == 3


def test_lease_expiring_during_page_rolls_back_suggestions(
    storage: Storage, settings: Settings
) -> None:
    now = [100.0]

    class SlowExtractor(RuleCountryExtractor):
        def extract_country(self, candidate: Candidate) -> Extraction | None:
            now[0] += 70
            return super().extract_country(candidate)

    with storage.transaction() as repository:
        run = repository.new_run(RunRequest())
    Worker(storage, settings, clock=lambda: now[0], extractor=SlowExtractor()).process_one()
    with storage.transaction() as repository:
        assert repository.get_run(run.id).processed == 0
        assert repository.suggestions(run.id) == []
    assert Worker(storage, settings, clock=lambda: now[0]).process_one()
    with storage.transaction() as repository:
        assert repository.get_run(run.id).processed == 3


def test_failed_page_retries_then_can_resume_after_fix(
    storage: Storage, settings: Settings, caplog: pytest.LogCaptureFixture
) -> None:
    now = [100.0]

    class BrokenExtractor:
        def extract_country(self, candidate: Candidate) -> Extraction | None:
            raise RuntimeError("private candidate data must not be logged")

    settings = replace(settings, max_attempts=2)
    worker = Worker(storage, settings, extractor=BrokenExtractor(), clock=lambda: now[0])
    with storage.transaction() as repository:
        run = repository.new_run(RunRequest())
    worker.process_one()
    assert not worker.process_one()
    now[0] += 3
    worker.process_one()
    with storage.transaction() as repository:
        failed = repository.get_run(run.id)
        assert failed.state == "failed" and failed.processed == 0
        assert "private candidate" not in str(failed.error)
        assert repository.suggestions(run.id) == []
        repository.change_run_state(run.id, "resume")
    assert "private candidate" not in caplog.text
    fixed = Worker(storage, settings, clock=lambda: now[0])
    for _ in range(10):
        if not fixed.process_one():
            break
    with storage.transaction() as repository:
        assert repository.get_run(run.id).state == "completed"


def test_concurrent_claims_have_one_owner(storage: Storage) -> None:
    with storage.transaction() as repository:
        repository.new_run(RunRequest())

    def claim() -> bool:
        with storage.transaction() as repository:
            return repository.claim_job(100, 60) is not None

    with ThreadPoolExecutor(max_workers=4) as pool:
        results = list(pool.map(lambda _: claim(), range(4)))
    assert sum(results) == 1


def test_large_run_uses_bounded_pages_and_excludes_new_candidates(
    storage: Storage, settings: Settings
) -> None:
    with storage.transaction() as repository:
        for index in range(2000, 3000):
            repository.add_candidate(
                Candidate(id=index, name="Synthetic load profile", address_country="AU")
            )
        repository.session.flush()
        run = repository.new_run(RunRequest())
        repository.add_candidate(
            Candidate(id=9999, name="Added after run start", address_country="NZ")
        )
    worker = Worker(storage, replace(settings, page_size=100))
    pages = 0
    last_processed = 0
    while worker.process_one():
        pages += 1
        with storage.transaction() as repository:
            current = repository.get_run(run.id)
        assert current.processed - last_processed <= 100
        last_processed = current.processed
        assert pages <= 12
    assert current.state == "completed"
    assert current.total == current.processed == 1010
    assert current.proposed == 1006
    assert current.cursor == 2999
