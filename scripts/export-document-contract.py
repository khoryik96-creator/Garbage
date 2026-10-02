"""Run with uv run --frozen python scripts/export-document-contract.py after a contract change."""

import json
from pathlib import Path

from garbage_document_worker.contracts import ExtractRequest, ExtractResponse

schema = {
    "protocol_version": 1,
    "request": ExtractRequest.model_json_schema(),
    "response": ExtractResponse.model_json_schema(),
}
destination = Path(__file__).resolve().parents[1] / "contracts/document-worker-v1.schema.json"
destination.write_text(json.dumps(schema, indent=2) + "\n", encoding="utf-8")
