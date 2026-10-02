from typing import Literal

from pydantic import BaseModel, ConfigDict, Field


class Contract(BaseModel):
    model_config = ConfigDict(extra="forbid", frozen=True)


class ExtractRequest(Contract):
    protocol_version: Literal[1]
    candidate_id: int = Field(ge=1)
    source_id: str = Field(min_length=1, max_length=200)
    text: str = Field(max_length=200_000)
    fields: list[Literal["country"]] = Field(min_length=1, max_length=1)


class Evidence(Contract):
    source: Literal["notes"]
    quote: str


class Extraction(Contract):
    value: str | None
    confidence: Literal["high", "low"]
    evidence: list[Evidence]
    reason: str


class ExtractResponse(Contract):
    protocol_version: Literal[1] = 1
    candidate_id: int
    source_id: str
    status: Literal["proposed", "not_found"]
    extraction: Extraction | None
