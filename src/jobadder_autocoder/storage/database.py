from collections.abc import Iterator
from contextlib import contextmanager
from pathlib import Path
from typing import Any

from alembic import command
from alembic.config import Config
from sqlalchemy import create_engine, event
from sqlalchemy.orm import sessionmaker

from jobadder_autocoder.storage.repository import Repository


class Storage:
    def __init__(self, url: str):
        self.engine = create_engine(url, connect_args={"check_same_thread": False, "timeout": 10})

        @event.listens_for(self.engine, "connect")
        def configure_sqlite(connection: Any, _record: Any) -> None:
            cursor = connection.cursor()
            cursor.execute("PRAGMA foreign_keys=ON")
            cursor.execute("PRAGMA journal_mode=WAL")
            cursor.execute("PRAGMA busy_timeout=10000")
            cursor.close()

        self.sessions = sessionmaker(self.engine, expire_on_commit=False)

    def migrate(self) -> None:
        config = Config()
        config.set_main_option("script_location", str(Path(__file__).parent / "migrations"))
        with self.engine.begin() as connection:
            config.attributes["connection"] = connection
            command.upgrade(config, "head")

    @contextmanager
    def transaction(self) -> Iterator[Repository]:
        with self.sessions() as session, session.begin():
            yield Repository(session)

    def close(self) -> None:
        self.engine.dispose()
