from fastapi.testclient import TestClient

from jobadder_autocoder.jobs.worker import Worker
from jobadder_autocoder.storage.database import Storage
from tests.conftest import make_run, proposal_for


def test_workspace_pages_and_assets_render(client: TestClient) -> None:
    for path in ["/", "/profiles", "/audit", "/static/app.css", "/static/app.js", "/api/docs"]:
        response = client.get(path)
        assert response.status_code == 200
    assert "Synthetic demo" in client.get("/").text
    assert client.get("/api/gaps").json() == {
        "field": "country",
        "total": 10,
        "missing": 8,
        "existing": 2,
        "mode": "synthetic",
    }


def test_api_preview_review_approval_and_undo(
    client: TestClient, worker: Worker, storage: Storage
) -> None:
    response = client.post("/api/runs", json={"mode": "review", "fields": ["country"]})
    assert response.status_code == 202
    run_id = response.json()["id"]
    for _ in range(5):
        worker.process_one()
    run = client.get(f"/api/runs/{run_id}").json()
    assert run["state"] == "completed" and run["processed"] == 10
    assert "Approve Country" in client.get(f"/runs/{run_id}").text
    suggestion_id = proposal_for(storage, run_id, 1001)
    approved = client.post(f"/api/suggestions/{suggestion_id}/approve", json={})
    assert approved.status_code == 200 and approved.json()["state"] == "applied"
    write_id = approved.json()["writeback_id"]
    undone = client.post(f"/api/writebacks/{write_id}/undo")
    assert undone.status_code == 200 and undone.json()["state"] == "undone"
    assert "Country Approved" in client.get("/audit").text


def test_html_forms_use_csrf_and_redirect(
    client: TestClient, worker: Worker, storage: Storage
) -> None:
    client.headers.pop("x-csrf-token")
    created = client.post(
        "/runs",
        data={"mode": "review", "fields": "country", "_csrf": client.cookies["gt_csrf"]},
        follow_redirects=False,
    )
    assert created.status_code == 303
    run_id = created.headers["location"].split("/")[-1]
    for _ in range(5):
        worker.process_one()
    suggestion_id = proposal_for(storage, run_id, 1001)
    approved = client.post(
        f"/suggestions/{suggestion_id}/approve",
        data={"value": "AU", "_csrf": client.cookies["gt_csrf"]},
        follow_redirects=False,
    )
    assert approved.status_code == 303
    assert client.get(approved.headers["location"]).status_code == 200


def test_preview_has_no_approval_controls(
    client: TestClient, storage: Storage, worker: Worker
) -> None:
    run = make_run(storage, worker, "preview")
    assert "Preview is read-only" in client.get(f"/runs/{run.id}").text
    assert "Approve Country" not in client.get(f"/runs/{run.id}").text


def test_csrf_and_origin_checks_block_browser_cross_site_changes(client: TestClient) -> None:
    token = client.headers.pop("x-csrf-token")
    assert client.post("/api/runs", json={}).status_code == 403
    client.headers["x-csrf-token"] = token
    assert (
        client.post("/api/runs", json={}, headers={"origin": "https://other.example"}).status_code
        == 403
    )
    assert client.get("/", headers={"host": "evil.example"}).status_code == 400


def test_api_rejects_unselected_fields_autofill_and_invalid_country(client: TestClient) -> None:
    for payload in [
        {"fields": ["email"]},
        {"mode": "auto_fill"},
        {"fields": []},
        {"overwrite": True},
    ]:
        assert client.post("/api/runs", json=payload).status_code == 422
    assert (
        client.post("/api/demo/candidates/1001/country", json={"country": "Atlantis"}).status_code
        == 422
    )
    assert (
        client.post(
            "/api/demo/candidates/1001/country", json={"country": "AU", "salary": 100}
        ).status_code
        == 422
    )
    assert client.get("/api/candidates?limit=10000").status_code == 422


def test_suggestions_have_complete_cursor_pagination(
    client: TestClient, storage: Storage, worker: Worker
) -> None:
    run = make_run(storage, worker)
    seen: set[str] = set()
    cursor = ""
    while True:
        result = client.get(
            f"/api/runs/{run.id}/suggestions", params={"limit": 2, "after": cursor}
        ).json()
        ids = {item["id"] for item in result["items"]}
        assert seen.isdisjoint(ids)
        seen.update(ids)
        cursor = result["next_cursor"]
        if cursor is None:
            break
    assert len(seen) == 6


def test_missing_resources_and_invalid_transitions_are_reported(client: TestClient) -> None:
    assert client.get("/runs/no-such-run").status_code == 404
    assert client.get("/api/runs/no-such-run").status_code == 404
    response = client.post("/api/runs", json={})
    run_id = response.json()["id"]
    assert client.post(f"/api/runs/{run_id}/resume").status_code == 409
