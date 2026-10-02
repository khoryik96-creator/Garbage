import logging
import time
from collections.abc import Callable

from jobadder_autocoder.connectors.demo import DemoConnector
from jobadder_autocoder.contracts.errors import LostLease
from jobadder_autocoder.contracts.models import RunRequest
from jobadder_autocoder.contracts.ports import CandidateGateway, Extractor
from jobadder_autocoder.pipeline.country import RuleCountryExtractor
from jobadder_autocoder.policy.country import may_suggest, validate_evidence
from jobadder_autocoder.settings import Settings
from jobadder_autocoder.storage.database import Storage
from jobadder_autocoder.storage.repository import Claim, Repository

logger = logging.getLogger(__name__)


class Worker:
    def __init__(
        self,
        storage: Storage,
        settings: Settings,
        *,
        extractor: Extractor | None = None,
        gateway_factory: Callable[[Repository], CandidateGateway] = DemoConnector,
        clock: Callable[[], float] = time.time,
    ):
        self.storage = storage
        self.settings = settings
        self.extractor = extractor or RuleCountryExtractor()
        self.gateway_factory = gateway_factory
        self.clock = clock

    def process_one(self) -> bool:
        with self.storage.transaction() as repository:
            claim = repository.claim_job(self.clock(), self.settings.lease_seconds)
        if claim is None:
            return False
        try:
            self.process_claim(claim)
        except LostLease:
            logger.info("A stale job was fenced off; the current owner will continue.")
        except Exception as exc:
            # Never include exception messages: provider errors can contain candidate data.
            error = f"Page processing failed ({type(exc).__name__})."
            logger.warning(error)
            try:
                with self.storage.transaction() as repository:
                    repository.retry_job(claim, self.clock(), self.settings.max_attempts, error)
            except LostLease:
                pass
        return True

    def process_claim(self, claim: Claim) -> None:
        with self.storage.transaction() as repository:
            repository.owned_job(claim, self.clock())
            run = repository.get_run(claim.run_id)
            if run.state not in {"queued", "running"}:
                repository.release_job(claim, self.clock())
                return
            gateway = self.gateway_factory(repository)
            page = gateway.page(
                after=run.cursor, through=run.upper_bound, limit=self.settings.page_size
            )
            missing = proposed = existing = not_found = 0
            request = RunRequest(mode=run.mode, fields=run.fields)
            for candidate in page.candidates:
                if not may_suggest(request, candidate):
                    existing += 1
                    continue
                missing += 1
                result = self.extractor.extract_country(candidate)
                if result is None:
                    not_found += 1
                else:
                    validate_evidence(candidate, result)
                    repository.add_suggestion(run, candidate, result)
                    proposed += 1
            # Suggestions, counters, and cursor share a transaction; a lost lease rolls it back.
            repository.checkpoint(
                claim,
                now=self.clock(),
                cursor=page.cursor,
                finished=page.finished,
                processed=len(page.candidates),
                missing=missing,
                proposed=proposed,
                existing=existing,
                not_found=not_found,
            )
