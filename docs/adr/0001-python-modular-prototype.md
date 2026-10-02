# 0001 · Python and a modular Country prototype

Status: accepted by the owner in the task conversation, 3 October 2026 (Malaysia).

## Context

The app will fill selected missing JobAdder fields across roughly 200,000 profiles.
Parsing and validating CV information are central; API quotas and external processing
are expected to dominate elapsed time. The owner chose the Python recommendation and
requested modular architecture that can scale.

## Decision

Use Python 3.12+, uv, FastAPI, Pydantic, SQLAlchemy, and Alembic. Deliver a local,
server-rendered prototype with a Country gap report, preview, review, audit, and guarded
undo over synthetic records. Maintain explicit domain contracts and provider interfaces.
Use a file-backed SQLite job table with one embedded worker by default; also support
an independently runnable worker. Do not introduce microservices or a frontend build.

## Consequences

The extraction ecosystem and shared schemas keep development focused. Modular
boundaries let a live connector, document parsers, and model providers be added without
placing their logic in routes. Go remains an option for a measured future bottleneck,
not a required component of the first release.

SQLite is appropriate for this local prototype. Postgres and worker scale-out require
tested concurrency and operational changes. The live API's update semantics must be
verified before promising the same atomic missing-field guarantee as the demo adapter.
No real profiles, API credentials, or model spend are required to validate this version.
