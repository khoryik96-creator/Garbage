import pycountry

from jobadder_autocoder.contracts.models import Candidate, Extraction, RunRequest, is_empty


class PolicyError(ValueError):
    pass


def normalize_country(value: str) -> str | None:
    text = value.strip()
    aliases = {"uk": "GB", "usa": "US", "south korea": "KR"}
    text = aliases.get(text.lower(), text)
    try:
        country = pycountry.countries.lookup(text)
    except LookupError:
        return None
    return str(country.alpha_2)


def validate_evidence(candidate: Candidate, extraction: Extraction) -> None:
    if not extraction.evidence:
        raise PolicyError("Every proposal needs source evidence.")
    for item in extraction.evidence:
        source = candidate.address_country if item.source == "address" else candidate.notes
        if not item.quote.strip() or item.quote not in (source or ""):
            raise PolicyError("The evidence quote does not occur in its source.")
    if extraction.value is not None:
        normalized = normalize_country(extraction.value)
        if normalized != extraction.value:
            raise PolicyError("The proposal must use a valid ISO country code.")


def may_suggest(request: RunRequest, candidate: Candidate) -> bool:
    return "country" in request.fields and is_empty(candidate.country)
