from typing import Any

from sqlalchemy import JSON, Float, ForeignKey, Index, Integer, String, Text, UniqueConstraint
from sqlalchemy.orm import DeclarativeBase, Mapped, mapped_column


class Base(DeclarativeBase):
    pass


class CandidateRow(Base):
    __tablename__ = "demo_candidates"
    id: Mapped[int] = mapped_column(Integer, primary_key=True, autoincrement=False)
    name: Mapped[str] = mapped_column(String(200))
    country: Mapped[str | None] = mapped_column(String(100), nullable=True)
    address_country: Mapped[str | None] = mapped_column(String(100), nullable=True)
    notes: Mapped[str] = mapped_column(Text)
    other_fields: Mapped[dict[str, str]] = mapped_column(JSON)
    version: Mapped[int] = mapped_column(Integer)


class RunRow(Base):
    __tablename__ = "runs"
    id: Mapped[str] = mapped_column(String(32), primary_key=True)
    mode: Mapped[str] = mapped_column(String(20))
    fields: Mapped[list[str]] = mapped_column(JSON)
    state: Mapped[str] = mapped_column(String(20))
    cursor: Mapped[int] = mapped_column(Integer)
    upper_bound: Mapped[int] = mapped_column(Integer)
    total: Mapped[int] = mapped_column(Integer)
    processed: Mapped[int] = mapped_column(Integer)
    missing: Mapped[int] = mapped_column(Integer)
    proposed: Mapped[int] = mapped_column(Integer)
    existing: Mapped[int] = mapped_column(Integer)
    not_found: Mapped[int] = mapped_column(Integer)
    created_at: Mapped[float] = mapped_column(Float)
    error: Mapped[str | None] = mapped_column(Text, nullable=True)


class JobRow(Base):
    __tablename__ = "jobs"
    id: Mapped[str] = mapped_column(ForeignKey("runs.id"), primary_key=True)
    state: Mapped[str] = mapped_column(String(20))
    attempts: Mapped[int] = mapped_column(Integer)
    available_at: Mapped[float] = mapped_column(Float)
    lease_until: Mapped[float | None] = mapped_column(Float, nullable=True)
    token: Mapped[str | None] = mapped_column(String(32), nullable=True)
    error: Mapped[str | None] = mapped_column(Text, nullable=True)
    __table_args__ = (Index("ix_jobs_ready", "state", "available_at"),)


class SuggestionRow(Base):
    __tablename__ = "suggestions"
    id: Mapped[str] = mapped_column(String(32), primary_key=True)
    run_id: Mapped[str] = mapped_column(ForeignKey("runs.id"))
    candidate_id: Mapped[int] = mapped_column(ForeignKey("demo_candidates.id"))
    field: Mapped[str] = mapped_column(String(50))
    value: Mapped[str | None] = mapped_column(String(2), nullable=True)
    confidence: Mapped[str] = mapped_column(String(20))
    evidence: Mapped[list[dict[str, Any]]] = mapped_column(JSON)
    reason: Mapped[str] = mapped_column(Text)
    state: Mapped[str] = mapped_column(String(20))
    __table_args__ = (
        UniqueConstraint("run_id", "candidate_id", "field", name="uq_run_candidate_field"),
        Index("ix_suggestions_run", "run_id", "id"),
    )


class WritebackRow(Base):
    __tablename__ = "writebacks"
    id: Mapped[str] = mapped_column(String(32), primary_key=True)
    suggestion_id: Mapped[str] = mapped_column(ForeignKey("suggestions.id"), unique=True)
    run_id: Mapped[str] = mapped_column(ForeignKey("runs.id"))
    candidate_id: Mapped[int] = mapped_column(ForeignKey("demo_candidates.id"))
    before_value: Mapped[str | None] = mapped_column(String(100), nullable=True)
    after_value: Mapped[str] = mapped_column(String(2))
    after_version: Mapped[int] = mapped_column(Integer)
    state: Mapped[str] = mapped_column(String(20))
    created_at: Mapped[float] = mapped_column(Float)


class AuditRow(Base):
    __tablename__ = "audit_events"
    id: Mapped[str] = mapped_column(String(32), primary_key=True)
    run_id: Mapped[str | None] = mapped_column(String(32), nullable=True)
    candidate_id: Mapped[int | None] = mapped_column(Integer, nullable=True)
    action: Mapped[str] = mapped_column(String(50))
    details: Mapped[dict[str, Any]] = mapped_column(JSON)
    created_at: Mapped[float] = mapped_column(Float, index=True)
