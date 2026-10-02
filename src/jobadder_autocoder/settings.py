import os
from dataclasses import dataclass
from pathlib import Path


@dataclass(frozen=True)
class Settings:
    database_url: str = "sqlite:///.data/autocoder.db"
    embedded_worker: bool = True
    page_size: int = 100
    lease_seconds: float = 60
    max_attempts: int = 5

    def __post_init__(self) -> None:
        if not 1 <= self.page_size <= 1000:
            raise ValueError("Page size must be between 1 and 1000.")
        if self.lease_seconds <= 0 or self.max_attempts < 1:
            raise ValueError("Lease duration and maximum attempts must be positive.")
        if not self.database_url.startswith("sqlite:///"):
            raise ValueError(
                "This prototype supports SQLite. PostgreSQL requires a tested deployment adapter."
            )
        path = self.database_url.removeprefix("sqlite:///")
        if path == ":memory:":
            raise ValueError("Use a file-backed database so jobs survive restarts.")
        Path(path).expanduser().parent.mkdir(parents=True, exist_ok=True)

    @classmethod
    def from_env(cls) -> "Settings":
        return cls(
            database_url=os.getenv("AUTOCODER_DATABASE_URL", "sqlite:///.data/autocoder.db"),
            embedded_worker=os.getenv("AUTOCODER_EMBEDDED_WORKER", "1") == "1",
            page_size=int(os.getenv("AUTOCODER_PAGE_SIZE", "100")),
        )
