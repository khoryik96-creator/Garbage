"""Initial synthetic prototype schema. Keep this revision immutable."""

import sqlalchemy as sa
from alembic import op

revision = "0001"
down_revision = None
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.create_table(
        "demo_candidates",
        sa.Column("id", sa.Integer(), primary_key=True, autoincrement=False),
        sa.Column("name", sa.String(200), nullable=False),
        sa.Column("country", sa.String(100)),
        sa.Column("address_country", sa.String(100)),
        sa.Column("notes", sa.Text(), nullable=False),
        sa.Column("other_fields", sa.JSON(), nullable=False),
        sa.Column("version", sa.Integer(), nullable=False),
    )
    op.create_table(
        "runs",
        sa.Column("id", sa.String(32), primary_key=True),
        sa.Column("mode", sa.String(20), nullable=False),
        sa.Column("fields", sa.JSON(), nullable=False),
        sa.Column("state", sa.String(20), nullable=False),
        *[
            sa.Column(name, sa.Integer(), nullable=False)
            for name in (
                "cursor",
                "upper_bound",
                "total",
                "processed",
                "missing",
                "proposed",
                "existing",
                "not_found",
            )
        ],
        sa.Column("created_at", sa.Float(), nullable=False),
        sa.Column("error", sa.Text()),
    )
    op.create_table(
        "jobs",
        sa.Column("id", sa.String(32), sa.ForeignKey("runs.id"), primary_key=True),
        sa.Column("state", sa.String(20), nullable=False),
        sa.Column("attempts", sa.Integer(), nullable=False),
        sa.Column("available_at", sa.Float(), nullable=False),
        sa.Column("lease_until", sa.Float()),
        sa.Column("token", sa.String(32)),
        sa.Column("error", sa.Text()),
    )
    op.create_index("ix_jobs_ready", "jobs", ["state", "available_at"])
    op.create_table(
        "suggestions",
        sa.Column("id", sa.String(32), primary_key=True),
        sa.Column("run_id", sa.String(32), sa.ForeignKey("runs.id"), nullable=False),
        sa.Column(
            "candidate_id", sa.Integer(), sa.ForeignKey("demo_candidates.id"), nullable=False
        ),
        sa.Column("field", sa.String(50), nullable=False),
        sa.Column("value", sa.String(2)),
        sa.Column("confidence", sa.String(20), nullable=False),
        sa.Column("evidence", sa.JSON(), nullable=False),
        sa.Column("reason", sa.Text(), nullable=False),
        sa.Column("state", sa.String(20), nullable=False),
        sa.UniqueConstraint("run_id", "candidate_id", "field", name="uq_run_candidate_field"),
    )
    op.create_index("ix_suggestions_run", "suggestions", ["run_id", "id"])
    op.create_table(
        "writebacks",
        sa.Column("id", sa.String(32), primary_key=True),
        sa.Column(
            "suggestion_id",
            sa.String(32),
            sa.ForeignKey("suggestions.id"),
            nullable=False,
            unique=True,
        ),
        sa.Column("run_id", sa.String(32), sa.ForeignKey("runs.id"), nullable=False),
        sa.Column(
            "candidate_id", sa.Integer(), sa.ForeignKey("demo_candidates.id"), nullable=False
        ),
        sa.Column("before_value", sa.String(100)),
        sa.Column("after_value", sa.String(2), nullable=False),
        sa.Column("after_version", sa.Integer(), nullable=False),
        sa.Column("state", sa.String(20), nullable=False),
        sa.Column("created_at", sa.Float(), nullable=False),
    )
    op.create_table(
        "audit_events",
        sa.Column("id", sa.String(32), primary_key=True),
        sa.Column("run_id", sa.String(32)),
        sa.Column("candidate_id", sa.Integer()),
        sa.Column("action", sa.String(50), nullable=False),
        sa.Column("details", sa.JSON(), nullable=False),
        sa.Column("created_at", sa.Float(), nullable=False),
    )
    op.create_index("ix_audit_events_created_at", "audit_events", ["created_at"])


def downgrade() -> None:
    for table in ["audit_events", "writebacks", "suggestions", "jobs", "runs", "demo_candidates"]:
        op.drop_table(table)
