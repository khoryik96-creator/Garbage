import pytest
from pydantic import ValidationError

from jobadder_autocoder.contracts.models import (
    Candidate,
    Evidence,
    Extraction,
    RunRequest,
    is_empty,
)
from jobadder_autocoder.pipeline.country import RuleCountryExtractor
from jobadder_autocoder.policy.country import PolicyError, normalize_country, validate_evidence


@pytest.mark.parametrize(
    "raw,code", [("Australia", "AU"), (" nz ", "NZ"), ("UK", "GB"), ("Atlantis", None)]
)
def test_country_normalization(raw: str, code: str | None) -> None:
    assert normalize_country(raw) == code


@pytest.mark.parametrize("value", [None, "", "   "])
def test_only_empty_values_are_missing(value: str | None) -> None:
    assert is_empty(value)


@pytest.mark.parametrize("value", ["Unknown", "N/A", "TBC", "-", "AU", "0"])
def test_placeholders_are_preserved(value: str) -> None:
    assert not is_empty(value)


@pytest.mark.parametrize(
    "payload",
    [
        {"fields": ["email"]},
        {"fields": []},
        {"fields": ["country", "country"]},
        {"mode": "auto_fill"},
        {"overwrite": True},
    ],
)
def test_unsupported_scope_is_rejected(payload: dict[str, object]) -> None:
    with pytest.raises(ValidationError):
        RunRequest.model_validate(payload)


def test_explicit_residence_has_verbatim_evidence() -> None:
    candidate = Candidate(id=1, name="Demo", notes="  Country of residence: New Zealand  ")
    result = RuleCountryExtractor().extract_country(candidate)
    assert result and result.value == "NZ"
    assert result.evidence[0].quote == candidate.notes
    validate_evidence(candidate, result)


def test_phone_nationality_and_previous_work_do_not_establish_residence() -> None:
    candidate = Candidate(
        id=1, name="Demo", notes="Phone: +61412345678\nNationality: Australian\nWorked in Australia"
    )
    assert RuleCountryExtractor().extract_country(candidate) is None


def test_conflicting_sources_require_a_human_choice() -> None:
    result = RuleCountryExtractor().extract_country(
        Candidate(id=1, name="Demo", address_country="AU", notes="Based in: New Zealand")
    )
    assert result and result.value is None and result.confidence == "low"
    assert len(result.evidence) == 2


def test_fabricated_evidence_cannot_enter_the_review_queue() -> None:
    candidate = Candidate(id=1, name="Demo", notes="No residence supplied.")
    result = Extraction(
        value="AU",
        confidence="high",
        evidence=[Evidence(source="notes", quote="Based in: Australia")],
        reason="Invalid fixture",
    )
    with pytest.raises(PolicyError, match="does not occur"):
        validate_evidence(candidate, result)
