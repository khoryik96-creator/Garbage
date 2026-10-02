from typing import Literal

from pydantic import BaseModel, ConfigDict, Field, field_validator

CountryField = Literal["country"]
RunMode = Literal["preview", "review"]
RunState = Literal["queued", "running", "paused", "completed", "cancelled", "failed"]
SuggestionState = Literal["preview", "pending", "applied", "rejected", "skipped"]


class Contract(BaseModel):
    model_config = ConfigDict(extra="forbid", frozen=True)


class Candidate(Contract):
    id: int
    name: str
    country: str | None = None
    address_country: str | None = None
    notes: str = ""
    other_fields: dict[str, str] = Field(default_factory=dict)
    version: int = 1


class CandidatePage(Contract):
    candidates: list[Candidate]
    cursor: int
    finished: bool


class Evidence(Contract):
    source: Literal["address", "notes"]
    quote: str


class Extraction(Contract):
    value: str | None
    confidence: Literal["high", "low"]
    evidence: list[Evidence]
    reason: str


class RunRequest(Contract):
    fields: list[CountryField] = Field(default=["country"], min_length=1)
    mode: RunMode = "preview"

    @field_validator("fields")
    @classmethod
    def unique_fields(cls, value: list[CountryField]) -> list[CountryField]:
        if len(value) != len(set(value)):
            raise ValueError("Select each field once.")
        return value


class Approval(Contract):
    value: str | None = None
    reason: str = Field(default="", max_length=500)


class RunView(Contract):
    id: str
    mode: RunMode
    fields: list[CountryField]
    state: RunState
    total: int
    processed: int
    missing: int
    proposed: int
    existing: int
    not_found: int
    cursor: int
    upper_bound: int
    created_at: float
    error: str | None = None


class SuggestionView(Contract):
    id: str
    run_id: str
    candidate_id: int
    candidate_name: str
    field: CountryField = "country"
    value: str | None
    confidence: Literal["high", "low"]
    evidence: list[Evidence]
    reason: str
    state: SuggestionState


class Mutation(Contract):
    applied: bool
    candidate: Candidate


def is_empty(value: str | None) -> bool:
    """Placeholders (including 'Unknown') remain existing values."""
    return value is None or not value.strip()
