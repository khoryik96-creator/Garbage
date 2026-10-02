import argparse
from collections.abc import Awaitable, Callable

import uvicorn
from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse, Response

from garbage_document_worker.contracts import ExtractRequest, ExtractResponse
from garbage_document_worker.extract import extract

app = FastAPI(title="Garbage Truck document worker", version="1", docs_url=None, redoc_url=None)


@app.middleware("http")
async def bound_request(
    request: Request, call_next: Callable[[Request], Awaitable[Response]]
) -> Response:
    chunks = []
    size = 0
    async for chunk in request.stream():
        size += len(chunk)
        if size > 1_048_576:
            return JSONResponse({"detail": "Document request too large."}, status_code=413)
        chunks.append(chunk)
    request._body = b"".join(chunks)
    return await call_next(request)


@app.get("/health")
def health() -> dict[str, str | int]:
    return {"status": "ok", "protocol_version": 1, "mode": "deterministic_text"}


@app.post("/v1/extract")
def extract_api(request: ExtractRequest) -> ExtractResponse:
    return extract(request)


def main() -> None:
    parser = argparse.ArgumentParser(description="Local document/AI processing boundary")
    parser.add_argument("--port", type=int, default=8002)
    args = parser.parse_args()
    uvicorn.run(app, host="127.0.0.1", port=args.port, access_log=False)
