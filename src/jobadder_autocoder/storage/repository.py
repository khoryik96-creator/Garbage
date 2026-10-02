import time
from dataclasses import dataclass
from typing import Any, cast
from uuid import uuid4

from sqlalchemy import and_, func, or_, select, update
from sqlalchemy.engine import CursorResult
from sqlalchemy.orm import Session

from jobadder_autocoder.contracts.errors import Conflict, LostLease, NotFound
from jobadder_autocoder.contracts.models import (
    Candidate,
    CandidatePage,
    Extraction,
    Mutation,
    RunRequest,
    RunView,
    SuggestionView,
)
from jobadder_autocoder.storage.models import (
    AuditRow,
    CandidateRow,
    JobRow,
    RunRow,
    SuggestionRow,
    WritebackRow,
)


@dataclass(frozen=True)
class Claim:
    run_id: str
    token: str
    attempt: int


class Repository:
    def __init__(self, session: Session):
        self.session = session

    @staticmethod
    def candidate_view(row: CandidateRow) -> Candidate:
        return Candidate.model_validate(
            {name: getattr(row, name) for name in Candidate.model_fields}
        )

    def add_candidate(self, candidate: Candidate) -> None:
        self.session.add(CandidateRow(**candidate.model_dump()))

    def get_candidate(self, candidate_id: int) -> Candidate:
        row = self.session.get(CandidateRow, candidate_id, populate_existing=True)
        if row is None:
            raise NotFound("Candidate not found.")
        return self.candidate_view(row)

    def page_candidates(self, after: int, through: int, limit: int) -> CandidatePage:
        rows = self.session.scalars(
            select(CandidateRow)
            .where(CandidateRow.id > after, CandidateRow.id <= through)
            .order_by(CandidateRow.id)
            .limit(limit)
        ).all()
        return CandidatePage(
            candidates=[self.candidate_view(row) for row in rows],
            cursor=rows[-1].id if rows else after,
            finished=len(rows) < limit,
        )

    def candidate_counts(self) -> dict[str, int]:
        missing = or_(CandidateRow.country.is_(None), func.trim(CandidateRow.country) == "")
        total = self.session.scalar(select(func.count()).select_from(CandidateRow)) or 0
        empty = (
            self.session.scalar(select(func.count()).select_from(CandidateRow).where(missing)) or 0
        )
        upper = self.session.scalar(select(func.max(CandidateRow.id))) or 0
        return {"total": total, "missing": empty, "existing": total - empty, "upper_bound": upper}

    def mutate_country(
        self, candidate: Candidate, value: str | None, *, expected_value: str | None = None
    ) -> Mutation:
        guard = (
            or_(CandidateRow.country.is_(None), func.trim(CandidateRow.country) == "")
            if expected_value is None
            else CandidateRow.country == expected_value
        )
        result = self.session.execute(
            update(CandidateRow)
            .where(
                CandidateRow.id == candidate.id, CandidateRow.version == candidate.version, guard
            )
            .values(country=value, version=CandidateRow.version + 1)
            .execution_options(synchronize_session=False)
        )
        return Mutation(
            applied=cast(CursorResult[Any], result).rowcount == 1,
            candidate=self.get_candidate(candidate.id),
        )

    def edit_demo_country(self, candidate_id: int, country: str | None) -> Candidate:
        self.get_candidate(candidate_id)
        self.session.execute(
            update(CandidateRow)
            .where(CandidateRow.id == candidate_id)
            .values(country=country, version=CandidateRow.version + 1)
            .execution_options(synchronize_session=False)
        )
        return self.get_candidate(candidate_id)

    def new_run(self, request: RunRequest) -> RunView:
        counts = self.candidate_counts()
        run_id = uuid4().hex
        self.session.add(
            RunRow(
                id=run_id,
                mode=request.mode,
                fields=request.fields,
                state="queued",
                cursor=0,
                upper_bound=counts["upper_bound"],
                total=counts["total"],
                processed=0,
                missing=0,
                proposed=0,
                existing=0,
                not_found=0,
                created_at=time.time(),
                error=None,
            )
        )
        self.session.flush()
        self.session.add(
            JobRow(
                id=run_id,
                state="queued",
                attempts=0,
                available_at=0,
                lease_until=None,
                token=None,
                error=None,
            )
        )
        self.audit("run_created", run_id, details=request.model_dump())
        return self.get_run(run_id)

    @staticmethod
    def run_view(row: RunRow) -> RunView:
        return RunView.model_validate({name: getattr(row, name) for name in RunView.model_fields})

    def get_run(self, run_id: str) -> RunView:
        row = self.session.get(RunRow, run_id, populate_existing=True)
        if row is None:
            raise NotFound("Run not found.")
        return self.run_view(row)

    def runs(self, limit: int = 30) -> list[RunView]:
        rows = self.session.scalars(select(RunRow).order_by(RunRow.created_at.desc()).limit(limit))
        return [self.run_view(row) for row in rows]

    def change_run_state(self, run_id: str, action: str) -> RunView:
        run = self.get_run(run_id)
        transitions = {
            "pause": ({"queued", "running"}, "paused"),
            "resume": ({"paused", "failed"}, "queued"),
            "cancel": ({"queued", "running", "paused", "failed"}, "cancelled"),
        }
        if action not in transitions:
            raise Conflict("Unknown run action.")
        allowed, state = transitions[action]
        if run.state not in allowed:
            raise Conflict(f"Cannot {action} a {run.state} run.")
        result = self.session.execute(
            update(RunRow)
            .where(RunRow.id == run_id, RunRow.state == run.state)
            .values(state=state, error=None)
            .execution_options(synchronize_session=False)
        )
        if cast(CursorResult[Any], result).rowcount != 1:
            raise Conflict("Run state changed. Refresh and try again.")
        if action == "resume":
            self.session.execute(
                update(JobRow)
                .where(JobRow.id == run_id)
                .values(state="queued", token=None, lease_until=None, available_at=0, attempts=0)
            )
        elif action == "cancel":
            self.session.execute(
                update(JobRow)
                .where(JobRow.id == run_id)
                .values(state="done", token=None, lease_until=None)
            )
        self.audit(f"run_{action}", run_id)
        return self.get_run(run_id)

    def add_suggestion(self, run: RunView, candidate: Candidate, result: Extraction) -> None:
        self.session.add(
            SuggestionRow(
                id=uuid4().hex,
                run_id=run.id,
                candidate_id=candidate.id,
                field="country",
                value=result.value,
                confidence=result.confidence,
                evidence=[item.model_dump() for item in result.evidence],
                reason=result.reason,
                state="preview" if run.mode == "preview" else "pending",
            )
        )

    def suggestion_view(self, row: SuggestionRow) -> SuggestionView:
        data = {
            name: getattr(row, name)
            for name in SuggestionView.model_fields
            if name != "candidate_name"
        }
        data["candidate_name"] = self.get_candidate(row.candidate_id).name
        return SuggestionView.model_validate(data)

    def get_suggestion(self, suggestion_id: str) -> SuggestionView:
        row = self.session.get(SuggestionRow, suggestion_id, populate_existing=True)
        if row is None:
            raise NotFound("Suggestion not found.")
        return self.suggestion_view(row)

    def suggestions(self, run_id: str, after: str = "", limit: int = 50) -> list[SuggestionView]:
        rows = self.session.scalars(
            select(SuggestionRow)
            .where(SuggestionRow.run_id == run_id, SuggestionRow.id > after)
            .order_by(SuggestionRow.id)
            .limit(limit)
        )
        return [self.suggestion_view(row) for row in rows]

    def set_suggestion_state(self, suggestion_id: str, expected: str, state: str) -> None:
        result = self.session.execute(
            update(SuggestionRow)
            .where(SuggestionRow.id == suggestion_id, SuggestionRow.state == expected)
            .values(state=state)
            .execution_options(synchronize_session=False)
        )
        if cast(CursorResult[Any], result).rowcount != 1:
            raise Conflict("This suggestion has already been reviewed.")

    def record_write(self, suggestion: SuggestionView, before: Candidate, after: Candidate) -> str:
        write_id = uuid4().hex
        self.session.add(
            WritebackRow(
                id=write_id,
                suggestion_id=suggestion.id,
                run_id=suggestion.run_id,
                candidate_id=suggestion.candidate_id,
                before_value=before.country,
                after_value=after.country,
                after_version=after.version,
                state="applied",
                created_at=time.time(),
            )
        )
        return write_id

    def get_write(self, write_id: str) -> WritebackRow:
        row = self.session.get(WritebackRow, write_id, populate_existing=True)
        if row is None:
            raise NotFound("Write-back not found.")
        return row

    def reserve_undo(self, write_id: str) -> WritebackRow:
        result = self.session.execute(
            update(WritebackRow)
            .where(WritebackRow.id == write_id, WritebackRow.state == "applied")
            .values(state="undoing")
            .execution_options(synchronize_session=False)
        )
        if cast(CursorResult[Any], result).rowcount != 1:
            raise Conflict("This write-back has already been undone or checked.")
        return self.get_write(write_id)

    def writes(self, run_id: str, limit: int = 50) -> list[dict[str, Any]]:
        rows = self.session.scalars(
            select(WritebackRow)
            .where(WritebackRow.run_id == run_id)
            .order_by(WritebackRow.created_at.desc())
            .limit(limit)
        )
        return [
            {column.name: getattr(row, column.name) for column in row.__table__.columns}
            for row in rows
        ]

    def audit(
        self,
        action: str,
        run_id: str | None = None,
        candidate_id: int | None = None,
        details: dict[str, Any] | None = None,
    ) -> None:
        self.session.add(
            AuditRow(
                id=uuid4().hex,
                run_id=run_id,
                candidate_id=candidate_id,
                action=action,
                details=details or {},
                created_at=time.time(),
            )
        )

    def audit_events(self, limit: int = 50, before: float | None = None) -> list[dict[str, Any]]:
        statement = select(AuditRow).order_by(AuditRow.created_at.desc()).limit(limit)
        if before is not None:
            statement = statement.where(AuditRow.created_at < before)
        rows = self.session.scalars(statement)
        return [
            {column.name: getattr(row, column.name) for column in row.__table__.columns}
            for row in rows
        ]

    def claim_job(self, now: float, lease_seconds: float) -> Claim | None:
        ready = or_(
            and_(JobRow.state == "queued", JobRow.available_at <= now),
            and_(JobRow.state == "leased", JobRow.lease_until <= now),
        )
        row = self.session.scalar(
            select(JobRow)
            .join(RunRow, RunRow.id == JobRow.id)
            .where(ready, RunRow.state.in_(["queued", "running"]))
            .order_by(JobRow.available_at, JobRow.id)
            .limit(1)
        )
        if row is None:
            return None
        token = uuid4().hex
        result = self.session.execute(
            update(JobRow)
            .where(JobRow.id == row.id, ready)
            .values(
                state="leased",
                token=token,
                lease_until=now + lease_seconds,
                attempts=JobRow.attempts + 1,
            )
            .execution_options(synchronize_session=False)
        )
        if cast(CursorResult[Any], result).rowcount != 1:
            return None
        return Claim(run_id=row.id, token=token, attempt=row.attempts + 1)

    def owned_job(self, claim: Claim, now: float) -> JobRow:
        row = self.session.get(JobRow, claim.run_id, populate_existing=True)
        if row is None or row.token != claim.token or row.state != "leased":
            raise LostLease("Job ownership changed.")
        if row.lease_until is None or row.lease_until <= now:
            raise LostLease("Job lease expired.")
        return row

    def checkpoint(
        self,
        claim: Claim,
        *,
        now: float,
        cursor: int,
        finished: bool,
        processed: int,
        missing: int,
        proposed: int,
        existing: int,
        not_found: int,
    ) -> None:
        self.owned_job(claim, now)
        self.session.execute(
            update(RunRow)
            .where(RunRow.id == claim.run_id)
            .values(
                cursor=cursor,
                state="completed" if finished else "running",
                processed=RunRow.processed + processed,
                missing=RunRow.missing + missing,
                proposed=RunRow.proposed + proposed,
                existing=RunRow.existing + existing,
                not_found=RunRow.not_found + not_found,
            )
        )
        job = self.owned_job(claim, now)
        job.state = "done" if finished else "queued"
        job.token, job.lease_until, job.attempts, job.available_at = None, None, 0, 0
        if finished:
            self.audit("run_completed", claim.run_id)

    def release_job(self, claim: Claim, now: float) -> None:
        job = self.owned_job(claim, now)
        job.state, job.token, job.lease_until = "queued", None, None

    def retry_job(self, claim: Claim, now: float, max_attempts: int, error: str) -> None:
        job = self.owned_job(claim, now)
        failed = job.attempts >= max_attempts
        job.state, job.token, job.lease_until = "failed" if failed else "queued", None, None
        job.available_at = now + min(60, 2 ** min(job.attempts, 6))
        job.error = error
        if failed:
            self.session.execute(
                update(RunRow).where(RunRow.id == claim.run_id).values(state="failed", error=error)
            )
            self.audit("run_failed", claim.run_id, details={"error": error})
