from collections.abc import Iterator
from pathlib import Path

import pytest
from fastapi.testclient import TestClient

from jobadder_autocoder.connectors.demo import seed_demo
from jobadder_autocoder.contracts.models import RunRequest, RunView
from jobadder_autocoder.jobs.worker import Worker
from jobadder_autocoder.settings import Settings
from jobadder_autocoder.storage.database import Storage
from jobadder_autocoder.web.app import create_app


@pytest.fixture
def settings(tmp_path: Path) -> Settings:
    return Settings(
        database_url=f"sqlite:///{tmp_path / 'test.db'}", embedded_worker=False, page_size=3
    )


@pytest.fixture
def storage(settings: Settings) -> Iterator[Storage]:
    store = Storage(settings.database_url)
    store.migrate()
    with store.transaction() as repository:
        seed_demo(repository)
    yield store
    store.close()


@pytest.fixture
def worker(storage: Storage, settings: Settings) -> Worker:
    return Worker(storage, settings)


@pytest.fixture
def client(settings: Settings, storage: Storage) -> Iterator[TestClient]:
    with TestClient(create_app(settings)) as client:
        assert client.get("/api/health").status_code == 200
        client.headers["x-csrf-token"] = client.cookies["gt_csrf"]
        yield client


def make_run(storage: Storage, worker: Worker, mode: str = "review") -> RunView:
    with storage.transaction() as repository:
        run = repository.new_run(RunRequest.model_validate({"mode": mode, "fields": ["country"]}))
    for _ in range(30):
        worker.process_one()
        with storage.transaction() as repository:
            run = repository.get_run(run.id)
        if run.state == "completed":
            return run
    raise AssertionError("The run did not complete.")


def proposal_for(storage: Storage, run_id: str, candidate_id: int) -> str:
    with storage.transaction() as repository:
        return next(s.id for s in repository.suggestions(run_id) if s.candidate_id == candidate_id)
