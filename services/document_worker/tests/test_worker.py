import json
from pathlib import Path

import pytest
from fastapi.testclient import TestClient

from garbage_document_worker.app import app
from garbage_document_worker.contracts import ExtractRequest, ExtractResponse

client = TestClient(app)
ROOT = Path(__file__).resolve().parents[3]
RESIDENCE_CASES = json.loads((ROOT / "contracts/residence-text-cases.json").read_text())


def payload(text: str) -> dict[str, object]:
    return {
        "protocol_version": 1,
        "candidate_id": 1003,
        "source_id": "candidate:1003:notes:v1",
        "text": text,
        "fields": ["country"],
    }


@pytest.mark.parametrize("case", RESIDENCE_CASES, ids=lambda case: case["name"])
def test_shared_residence_text_contract(case: dict[str, object]) -> None:
    response = client.post("/v1/extract", json=payload(str(case["text"])))
    assert response.status_code == 200
    result = response.json()
    if not case["quotes"]:
        assert result["status"] == "not_found" and result["extraction"] is None
        return
    assert result["status"] == "proposed"
    assert result["extraction"]["value"] == case["value"]
    assert result["extraction"]["confidence"] == ("high" if case["value"] else "low")
    assert result["extraction"]["evidence"] == [
        {"source": "notes", "quote": quote} for quote in case["quotes"]
    ]


@pytest.mark.parametrize("value,code", [("Malaysia", "MY"), ("UK", "GB"), ("NZ", "NZ")])
def test_explicit_residence_preserves_source_and_identity(value: str, code: str) -> None:
    text = f"  Country of residence: {value}  "
    response = client.post("/v1/extract", json=payload(text))
    assert response.status_code == 200
    result = response.json()
    assert result["candidate_id"] == 1003
    assert result["source_id"] == "candidate:1003:notes:v1"
    assert result["extraction"]["value"] == code
    assert result["extraction"]["evidence"] == [{"source": "notes", "quote": text}]


@pytest.mark.parametrize(
    "text", ["Nationality: Malaysian", "Previously worked in Australia", "+60123456789", ""]
)
def test_does_not_guess_residence(text: str) -> None:
    result = client.post("/v1/extract", json=payload(text)).json()
    assert result["status"] == "not_found" and result["extraction"] is None


@pytest.mark.parametrize(
    "text", ["Based in: Atlantis", "Based in: Malaysia\nResidence country: Singapore"]
)
def test_ambiguous_sources_require_review(text: str) -> None:
    result = client.post("/v1/extract", json=payload(text)).json()
    assert result["extraction"]["value"] is None
    assert result["extraction"]["confidence"] == "low"


@pytest.mark.parametrize(
    "changes",
    [
        {"protocol_version": 2},
        {"fields": ["email"]},
        {"fields": ["country", "country"]},
        {"candidate_id": 0},
        {"write": True},
        {"text": "x" * 200_001},
    ],
)
def test_rejects_unsupported_contract(changes: dict[str, object]) -> None:
    request = payload("Based in: Malaysia") | changes
    assert client.post("/v1/extract", json=request).status_code == 422


def test_request_size_and_worker_scope() -> None:
    assert client.post("/v1/extract", content=b"x" * 1_048_577).status_code == 413
    assert client.get("/health").json()["mode"] == "deterministic_text"
    assert client.post("/api/runs", json={}).status_code == 404


def test_published_contract_matches_worker_models() -> None:
    root = Path(__file__).resolve().parents[3]
    contract = json.loads((root / "contracts/document-worker-v1.schema.json").read_text())
    assert contract["protocol_version"] == 1
    assert contract["request"] == ExtractRequest.model_json_schema()
    assert contract["response"] == ExtractResponse.model_json_schema()
