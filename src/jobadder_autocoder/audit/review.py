from collections.abc import Callable
from typing import Any

from jobadder_autocoder.connectors.demo import DemoConnector
from jobadder_autocoder.contracts.errors import Conflict
from jobadder_autocoder.contracts.models import Approval, Extraction, is_empty
from jobadder_autocoder.contracts.ports import CandidateGateway
from jobadder_autocoder.policy.country import PolicyError, normalize_country, validate_evidence
from jobadder_autocoder.storage.database import Storage
from jobadder_autocoder.storage.repository import Repository


class ReviewService:
    def __init__(
        self,
        storage: Storage,
        gateway_factory: Callable[[Repository], CandidateGateway] = DemoConnector,
    ):
        self.storage = storage
        self.gateway_factory = gateway_factory

    def approve(self, suggestion_id: str, approval: Approval) -> dict[str, Any]:
        with self.storage.transaction() as repository:
            suggestion = repository.get_suggestion(suggestion_id)
            run = repository.get_run(suggestion.run_id)
            if run.mode != "review" or run.state != "completed" or "country" not in run.fields:
                raise Conflict("Only a completed review run permits approvals.")
            value = normalize_country(approval.value or suggestion.value or "")
            if value is None:
                raise PolicyError("Choose a valid country before approving.")
            if value != suggestion.value and not approval.reason.strip():
                raise PolicyError("Explain the country correction so it can be audited.")
            repository.set_suggestion_state(suggestion.id, "pending", "applied")
            gateway = self.gateway_factory(repository)
            before = gateway.get(suggestion.candidate_id)
            if not is_empty(before.country):
                repository.set_suggestion_state(suggestion.id, "applied", "skipped")
                repository.audit(
                    "approval_skipped", run.id, before.id, {"reason": "Country was already filled."}
                )
                return {"state": "skipped", "writeback_id": None}
            validate_evidence(
                before,
                Extraction(
                    value=suggestion.value,
                    confidence=suggestion.confidence,
                    evidence=suggestion.evidence,
                    reason=suggestion.reason,
                ),
            )
            mutation = gateway.fill_country(before, value)
            if not mutation.applied:
                repository.set_suggestion_state(suggestion.id, "applied", "skipped")
                repository.audit(
                    "approval_skipped",
                    run.id,
                    before.id,
                    {"reason": "Candidate changed during approval."},
                )
                return {"state": "skipped", "writeback_id": None}
            after = mutation.candidate
            if (
                after.model_dump(exclude={"country", "version"})
                != before.model_dump(exclude={"country", "version"})
                or after.country != value
            ):
                raise PolicyError("Unexpected field changes. Approval was rolled back.")
            write_id = repository.record_write(suggestion, before, after)
            repository.audit(
                "country_approved",
                run.id,
                before.id,
                {
                    "field": "country",
                    "before": before.country,
                    "after": value,
                    "writeback_id": write_id,
                    "reason": approval.reason,
                    "actor": "local reviewer",
                },
            )
            return {"state": "applied", "writeback_id": write_id}

    def reject(self, suggestion_id: str) -> None:
        with self.storage.transaction() as repository:
            suggestion = repository.get_suggestion(suggestion_id)
            run = repository.get_run(suggestion.run_id)
            if run.mode != "review" or run.state != "completed":
                raise Conflict("Only a completed review run permits rejections.")
            repository.set_suggestion_state(suggestion.id, "pending", "rejected")
            repository.audit("country_rejected", run.id, suggestion.candidate_id)

    def undo(self, write_id: str) -> dict[str, str]:
        with self.storage.transaction() as repository:
            write = repository.get_write(write_id)
            if write.state != "applied":
                raise Conflict("This write-back has already been undone or checked.")
            write = repository.reserve_undo(write_id)
            gateway = self.gateway_factory(repository)
            current = gateway.get(write.candidate_id)
            if not is_empty(write.before_value):
                raise PolicyError("Undo must restore the original empty value.")
            if current.country != write.after_value or current.version != write.after_version:
                write.state = "undo_skipped"
            else:
                result = gateway.clear_country(current, write.after_value, write.before_value)
                write.state = "undone" if result.applied else "undo_skipped"
            repository.audit(
                "country_undo",
                write.run_id,
                write.candidate_id,
                {"writeback_id": write.id, "state": write.state},
            )
            return {"state": write.state}
