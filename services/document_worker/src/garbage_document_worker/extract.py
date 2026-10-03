import re

import pycountry

from garbage_document_worker.contracts import Evidence, Extraction, ExtractRequest, ExtractResponse

RESIDENCE = re.compile(
    r"^(?:country of residence|residence country|based in)\s*:\s*(.+?)\s*$", re.IGNORECASE
)


def normalize(value: str) -> str | None:
    text = value.strip()
    text = {"uk": "GB", "usa": "US", "south korea": "KR"}.get(text.lower(), text)
    try:
        return str(pycountry.countries.lookup(text).alpha_2)
    except LookupError:
        return None


def extract(request: ExtractRequest) -> ExtractResponse:
    evidence: list[Evidence] = []
    values: set[str] = set()
    invalid = False
    for line in request.text.splitlines():
        match = RESIDENCE.fullmatch(line.strip())
        if match:
            evidence.append(Evidence(source="notes", quote=line))
            code = normalize(match.group(1))
            if code:
                values.add(code)
            else:
                invalid = True
    result = None
    if evidence:
        certain = not invalid and len(values) == 1
        result = Extraction(
            value=next(iter(values)) if certain else None,
            confidence="high" if certain else "low",
            evidence=evidence,
            reason=(
                "An explicit country of residence was found in the source."
                if certain
                else "Sources conflict or contain an unrecognized country. Review required."
            ),
        )
    return ExtractResponse(
        candidate_id=request.candidate_id,
        source_id=request.source_id,
        status="proposed" if result else "not_found",
        extraction=result,
    )
