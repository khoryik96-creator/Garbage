import argparse
import time

import uvicorn

from jobadder_autocoder.connectors.demo import seed_demo
from jobadder_autocoder.jobs.worker import Worker
from jobadder_autocoder.settings import Settings
from jobadder_autocoder.storage.database import Storage


def initialize(storage: Storage) -> None:
    storage.migrate()
    with storage.transaction() as repository:
        seed_demo(repository)


def serve() -> None:
    parser = argparse.ArgumentParser(description="Start the synthetic Country prototype.")
    parser.add_argument("--port", type=int, default=8000)
    parser.add_argument(
        "--init-only", action="store_true", help="Migrate and seed without starting."
    )
    args = parser.parse_args()
    if args.init_only:
        storage = Storage(Settings.from_env().database_url)
        try:
            initialize(storage)
        finally:
            storage.close()
        print("Database initialized. Synthetic profiles are preserved on repeat runs.")
        return
    uvicorn.run(
        "jobadder_autocoder.web.app:create_app",
        factory=True,
        host="127.0.0.1",
        port=args.port,
        access_log=False,
    )


def worker() -> None:
    settings = Settings.from_env()
    storage = Storage(settings.database_url)
    initialize(storage)
    runner = Worker(storage, settings)
    try:
        while True:
            runner.process_one()
            time.sleep(0.25)
    except KeyboardInterrupt:
        pass
    finally:
        storage.close()
