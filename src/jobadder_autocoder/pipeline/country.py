import re

from jobadder_autocoder.contracts.models import Candidate, Evidence, Extraction
from jobadder_autocoder.policy.country import normalize_country

# Deliberately narrow: explicit country of residence, never nationality, phone origin,
# previous employment, city guesses, or an unverified LLM response.
RESIDENCE = re.compile(
    r"^(?:country of residence|residence country|based in)\s*:\s*(.+?)\s*$", re.IGNORECASE
)


class RuleCountryExtractor:
    def extract_country(self, candidate: Candidate) -> Extraction | None:
        evidence: list[Evidence] = []
        values: set[str] = set()
        invalid = False
        if candidate.address_country and candidate.address_country.strip():
            evidence.append(Evidence(source="address", quote=candidate.address_country))
            code = normalize_country(candidate.address_country)
            invalid = code is None
            if code:
                values.add(code)
        for line in candidate.notes.splitlines():
            match = RESIDENCE.fullmatch(line.strip())
            if match:
                evidence.append(Evidence(source="notes", quote=line))
                code = normalize_country(match.group(1))
                if code:
                    values.add(code)
                else:
                    invalid = True
        if not evidence:
            return None
        if invalid or len(values) != 1:
            return Extraction(
                value=None,
                confidence="low",
                evidence=evidence,
                reason="Sources conflict or contain an unrecognized country. Review required.",
            )
        return Extraction(
            value=values.pop(),
            confidence="high",
            evidence=evidence,
            reason="An explicit country of residence was found in the source.",
        )
