import asyncio
import hmac
import secrets
from collections.abc import AsyncIterator, Awaitable, Callable
from contextlib import asynccontextmanager
from datetime import datetime
from pathlib import Path
from typing import Annotated, Any, Literal
from zoneinfo import ZoneInfo

import pycountry
from fastapi import Depends, FastAPI, Form, HTTPException, Query, Request
from fastapi.responses import HTMLResponse, JSONResponse, RedirectResponse, Response
from fastapi.staticfiles import StaticFiles
from fastapi.templating import Jinja2Templates
from pydantic import Field
from starlette.middleware.trustedhost import TrustedHostMiddleware

from jobadder_autocoder.audit.review import ReviewService
from jobadder_autocoder.connectors.demo import seed_demo
from jobadder_autocoder.contracts.errors import Conflict, NotFound
from jobadder_autocoder.contracts.models import Approval, Contract, RunRequest, RunView
from jobadder_autocoder.jobs.worker import Worker
from jobadder_autocoder.policy.country import PolicyError, normalize_country
from jobadder_autocoder.settings import Settings
from jobadder_autocoder.storage.database import Storage

ASSETS = Path(__file__).parent
COUNTRIES = sorted([(str(c.alpha_2), str(c.name)) for c in pycountry.countries], key=lambda c: c[1])
COUNTRY_NAMES = dict(COUNTRIES)


class DemoCountryEdit(Contract):
    country: str | None = Field(default=None, max_length=100)


async def require_csrf(request: Request) -> None:
    if request.method in {"GET", "HEAD", "OPTIONS"}:
        return
    origin = request.headers.get("origin")
    if origin and origin.rstrip("/") != str(request.base_url).rstrip("/"):
        raise HTTPException(403, "Cross-origin changes are not allowed.")
    token = request.headers.get("x-csrf-token")
    if not token and request.headers.get("content-type", "").startswith(
        "application/x-www-form-urlencoded"
    ):
        token = str((await request.form()).get("_csrf", ""))
    cookie = request.cookies.get("gt_csrf", "")
    if not cookie or not token or not hmac.compare_digest(cookie, token):
        raise HTTPException(403, "Refresh the page before submitting changes.")


def create_app(settings: Settings | None = None) -> FastAPI:
    settings = settings or Settings.from_env()
    storage = Storage(settings.database_url)
    worker = Worker(storage, settings)
    review = ReviewService(storage)

    @asynccontextmanager
    async def lifespan(_app: FastAPI) -> AsyncIterator[None]:
        storage.migrate()
        with storage.transaction() as repository:
            seed_demo(repository)
        stop = asyncio.Event()

        async def work() -> None:
            while not stop.is_set():
                try:
                    await asyncio.to_thread(worker.process_one)
                except Exception:
                    # Claim failures are retried on the next tick; no personal data in logs.
                    import logging

                    logging.getLogger(__name__).warning("Queue claim unavailable; retrying.")
                try:
                    await asyncio.wait_for(stop.wait(), timeout=0.25)
                except TimeoutError:
                    pass

        task = asyncio.create_task(work()) if settings.embedded_worker else None
        try:
            yield
        finally:
            stop.set()
            if task:
                await task
            storage.close()

    app = FastAPI(
        title="Garbage Truck · JobAdder auto-coder",
        version="0.1.0",
        lifespan=lifespan,
        dependencies=[Depends(require_csrf)],
        docs_url="/api/docs",
        openapi_url="/api/openapi.json",
        redoc_url=None,
    )
    app.state.storage, app.state.worker, app.state.review = storage, worker, review
    app.add_middleware(
        TrustedHostMiddleware, allowed_hosts=["localhost", "127.0.0.1", "testserver"]
    )
    app.mount("/static", StaticFiles(directory=ASSETS / "static"), name="static")
    templates = Jinja2Templates(directory=ASSETS / "templates")
    templates.env.filters["country"] = lambda value: COUNTRY_NAMES.get(value, value or "Missing")
    templates.env.filters["timestamp"] = lambda value: datetime.fromtimestamp(
        value, ZoneInfo("Asia/Kuala_Lumpur")
    ).strftime("%d %b %Y · %H:%M MYT")

    @app.middleware("http")
    async def browser_headers(
        request: Request, call_next: Callable[[Request], Awaitable[Response]]
    ) -> Response:
        request.state.csrf = request.cookies.get("gt_csrf") or secrets.token_urlsafe(32)
        response = await call_next(request)
        if not request.cookies.get("gt_csrf"):
            response.set_cookie("gt_csrf", request.state.csrf, httponly=True, samesite="strict")
        response.headers["X-Content-Type-Options"] = "nosniff"
        response.headers["Cache-Control"] = "no-store"
        response.headers["Referrer-Policy"] = "same-origin"
        if request.url.path != "/api/docs":
            response.headers["Content-Security-Policy"] = (
                "default-src 'self'; style-src 'self'; script-src 'self'; "
                "img-src 'self' data:; form-action 'self'; frame-ancestors 'none'; base-uri 'none'"
            )
        return response

    def render(request: Request, name: str, **context: Any) -> HTMLResponse:
        return templates.TemplateResponse(
            request=request,
            name=name,
            context={"csrf": request.state.csrf, "countries": COUNTRIES, **context},
        )

    async def domain_error(request: Request, exc: Exception) -> Response:
        status = 404 if isinstance(exc, NotFound) else 422 if isinstance(exc, PolicyError) else 409
        if request.url.path.startswith("/api/"):
            return JSONResponse({"detail": str(exc)}, status_code=status)
        response = render(request, "error.html", message=str(exc))
        response.status_code = status
        return response

    for error in [NotFound, Conflict, PolicyError]:
        app.add_exception_handler(error, domain_error)

    @app.get("/api/health")
    def health() -> dict[str, Any]:
        with storage.transaction() as repository:
            repository.candidate_counts()
        return {
            "status": "ok",
            "mode": "synthetic",
            "database": "ok",
            "embedded_worker": settings.embedded_worker,
        }

    @app.get("/api/gaps")
    def gaps() -> dict[str, Any]:
        with storage.transaction() as repository:
            counts = repository.candidate_counts()
        return {
            "field": "country",
            "total": counts["total"],
            "missing": counts["missing"],
            "existing": counts["existing"],
            "mode": "synthetic",
        }

    @app.get("/api/candidates")
    def candidates_api(
        after: Annotated[int, Query(ge=0)] = 0,
        limit: Annotated[int, Query(ge=1, le=100)] = 50,
    ) -> dict[str, Any]:
        with storage.transaction() as repository:
            page = repository.page_candidates(
                after, repository.candidate_counts()["upper_bound"], limit
            )
        return page.model_dump()

    @app.post("/api/demo/candidates/{candidate_id}/country")
    def edit_demo(candidate_id: int, edit: DemoCountryEdit) -> dict[str, Any]:
        country = normalize_country(edit.country) if edit.country and edit.country.strip() else None
        if edit.country and edit.country.strip() and not country:
            raise PolicyError("Choose a valid country or clear the field.")
        with storage.transaction() as repository:
            candidate = repository.edit_demo_country(candidate_id, country)
            repository.audit(
                "demo_country_edited", candidate_id=candidate_id, details={"country": country}
            )
        return candidate.model_dump()

    @app.get("/api/runs")
    def runs_api() -> list[RunView]:
        with storage.transaction() as repository:
            return repository.runs()

    @app.post("/api/runs", status_code=202)
    def create_run_api(payload: RunRequest) -> RunView:
        with storage.transaction() as repository:
            return repository.new_run(payload)

    @app.get("/api/runs/{run_id}")
    def run_api(run_id: str) -> RunView:
        with storage.transaction() as repository:
            return repository.get_run(run_id)

    @app.post("/api/runs/{run_id}/{action}")
    def run_action_api(run_id: str, action: Literal["pause", "resume", "cancel"]) -> RunView:
        with storage.transaction() as repository:
            return repository.change_run_state(run_id, action)

    @app.get("/api/runs/{run_id}/suggestions")
    def suggestions_api(
        run_id: str,
        after: str = "",
        limit: Annotated[int, Query(ge=1, le=100)] = 50,
    ) -> dict[str, Any]:
        with storage.transaction() as repository:
            repository.get_run(run_id)
            items = repository.suggestions(run_id, after, limit + 1)
        return {
            "items": [item.model_dump() for item in items[:limit]],
            "next_cursor": items[limit - 1].id if len(items) > limit else None,
        }

    @app.post("/api/suggestions/{suggestion_id}/approve")
    def approve_api(suggestion_id: str, payload: Approval) -> dict[str, Any]:
        return review.approve(suggestion_id, payload)

    @app.post("/api/suggestions/{suggestion_id}/reject")
    def reject_api(suggestion_id: str) -> dict[str, str]:
        review.reject(suggestion_id)
        return {"state": "rejected"}

    @app.post("/api/writebacks/{write_id}/undo")
    def undo_api(write_id: str) -> dict[str, str]:
        return review.undo(write_id)

    @app.get("/api/runs/{run_id}/writebacks")
    def writes_api(run_id: str) -> list[dict[str, Any]]:
        with storage.transaction() as repository:
            repository.get_run(run_id)
            return repository.writes(run_id)

    @app.get("/")
    def dashboard(request: Request) -> HTMLResponse:
        with storage.transaction() as repository:
            return render(
                request,
                "dashboard.html",
                counts=repository.candidate_counts(),
                runs=repository.runs(),
            )

    @app.post("/runs")
    def create_run_form(
        mode: Annotated[Literal["preview", "review"], Form()],
        fields: Annotated[list[Literal["country"]], Form()],
    ) -> RedirectResponse:
        with storage.transaction() as repository:
            run = repository.new_run(RunRequest(mode=mode, fields=fields))
        return RedirectResponse(f"/runs/{run.id}", 303)

    @app.get("/runs/{run_id}")
    def run_page(request: Request, run_id: str, after: str = "") -> HTMLResponse:
        with storage.transaction() as repository:
            run = repository.get_run(run_id)
            items = repository.suggestions(run_id, after, 51)
            return render(
                request,
                "run.html",
                run=run,
                suggestions=items[:50],
                next_cursor=items[49].id if len(items) > 50 else None,
                writes=repository.writes(run_id),
            )

    @app.post("/runs/{run_id}/{action}")
    def run_action_form(
        run_id: str, action: Literal["pause", "resume", "cancel"]
    ) -> RedirectResponse:
        with storage.transaction() as repository:
            repository.change_run_state(run_id, action)
        return RedirectResponse(f"/runs/{run_id}", 303)

    @app.post("/suggestions/{suggestion_id}/approve")
    def approve_form(
        suggestion_id: str,
        value: Annotated[str, Form()] = "",
        reason: Annotated[str, Form()] = "",
    ) -> RedirectResponse:
        review.approve(suggestion_id, Approval(value=value or None, reason=reason))
        with storage.transaction() as repository:
            run_id = repository.get_suggestion(suggestion_id).run_id
        return RedirectResponse(f"/runs/{run_id}", 303)

    @app.post("/suggestions/{suggestion_id}/reject")
    def reject_form(suggestion_id: str) -> RedirectResponse:
        review.reject(suggestion_id)
        with storage.transaction() as repository:
            run_id = repository.get_suggestion(suggestion_id).run_id
        return RedirectResponse(f"/runs/{run_id}", 303)

    @app.post("/writebacks/{write_id}/undo")
    def undo_form(write_id: str) -> RedirectResponse:
        review.undo(write_id)
        with storage.transaction() as repository:
            run_id = repository.get_write(write_id).run_id
        return RedirectResponse(f"/runs/{run_id}", 303)

    @app.get("/profiles")
    def profiles(request: Request, after: Annotated[int, Query(ge=0)] = 0) -> HTMLResponse:
        with storage.transaction() as repository:
            page = repository.page_candidates(
                after, repository.candidate_counts()["upper_bound"], 100
            )
        return render(request, "profiles.html", page=page)

    @app.get("/audit")
    def audit_page(request: Request, before: float | None = None) -> HTMLResponse:
        with storage.transaction() as repository:
            items = repository.audit_events(51, before)
        return render(
            request,
            "audit.html",
            events=items[:50],
            next_before=items[49]["created_at"] if len(items) > 50 else None,
        )

    return app
