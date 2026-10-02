from typing import Protocol

from jobadder_autocoder.contracts.models import Candidate, CandidatePage, Extraction, Mutation


class CandidateGateway(Protocol):
    def page(self, *, after: int, through: int, limit: int) -> CandidatePage: ...

    def get(self, candidate_id: int) -> Candidate: ...

    def fill_country(self, candidate: Candidate, value: str) -> Mutation: ...

    def clear_country(
        self, candidate: Candidate, expected_value: str, restore_value: str | None
    ) -> Mutation: ...


class Extractor(Protocol):
    def extract_country(self, candidate: Candidate) -> Extraction | None: ...
