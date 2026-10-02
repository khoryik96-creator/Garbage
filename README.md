# Garbage Truck · JobAdder auto-coder

A working Python prototype of the Country workflow in [Claude's brief](IDEATION.md).
It includes a gap report, read-only previews, human review, audit history, guarded undo,
and a persistent job queue. The interface runs locally and uses **synthetic profiles only**.
There is no JobAdder connection, CV parser, LLM call, or auto-fill mode in this version.

## Run it

Install Python 3.12+ and [uv](https://docs.astral.sh/uv/getting-started/installation/).
From this repository's root, run these commands on Windows, macOS, or Linux:

```sh
uv sync --frozen
uv run --frozen jobadder-autocoder
```

The terminal reports the local address, normally `http://127.0.0.1:8000`.
To use another port, add `--port 8001`. The server binds to loopback only.

On first startup, Alembic creates `.data/autocoder.db` and adds ten synthetic profiles.
Subsequent starts preserve profiles, runs, reviews, and audit events. `.data/` is ignored
by Git; do not commit database files or real candidate information.

To initialize without starting a server:

```sh
uv run --frozen jobadder-autocoder --init-only
```

## Try the first workflow

1. The workspace shows 10 profiles: 8 have missing Country and 2 have existing values.
2. Start a **Preview** run. It scans every profile, reports the gaps, and produces 6
   proposals: 4 contain explicit valid residence evidence and 2 need a human decision.
   The other 2 missing profiles have no residence evidence. Preview never changes a profile.
3. Start a **Review** run. After it completes, inspect each proposal and its source quote.
4. Approve a valid Country, reject a proposal, or choose a correction and explain it.
   The conflict example requires a correction note before it can be approved.
5. Check **Profiles** and **Audit trail**. Approved changes offer **Undo** on their run page.
   Undo is skipped if the profile was edited after the approval.

Only Country is selectable. Values such as `Unknown`, `N/A`, `TBC`, and `-` count as
existing data; the prototype does not replace them. Country is derived only from an
explicit address country or a labelled country-of-residence statement. Phone origin,
nationality, previous employment, and city guesses are not residence evidence.

Pause, resume, and cancel controls appear while a run is active. The ten-profile
example may finish before you can pause it; the tests exercise these controls over
multiple pages. Reloading the page or restarting the server does not reset a run.

## Architecture

This is a modular monolith, with an independently runnable worker:

```text
src/jobadder_autocoder/
  contracts/    Candidate, run, evidence, suggestion models; provider interfaces
  connectors/   Synthetic candidate adapter; future JobAdder adapter belongs here
  pipeline/     Deterministic Country extraction; future parsers and LLM adapters
  policy/       Missing-value policy, field selection, evidence validation
  jobs/         Leased durable jobs, bounded pages, retries, checkpoint recovery
  storage/      SQLAlchemy repositories, transactions, Alembic migrations
  audit/        Approval, rejection, audited write-back, guarded undo
  web/          FastAPI endpoints, server-rendered templates, static assets
```

Domain contracts import neither SQLAlchemy nor FastAPI. The worker consumes the
`CandidateGateway` and `Extractor` interfaces; review consumes the same gateway.
The web layer calls the application operations rather than implementing coding rules.
See [the architecture decision](docs/adr/0001-python-modular-prototype.md) and
[the scale and integration plan](docs/architecture.md).

[The Kano reference](docs/kano-reference.md) documents 20 observed field mappings
and the distinction between public OAuth lookup and browser API updates. The
versioned catalogue is available at `/api/fields`. Other fields remain disabled
for runs until their extraction, preservation policy, and public API mapping are tested.

## Persistent jobs and separate workers

Each run captures its initial candidate boundary and processes at most 100 profiles
per transaction. New profiles are picked up by a later run. Suggestions, counters, and
the progress cursor commit together. Expired leases can be reclaimed; obsolete owners
cannot commit their page. The unique run/candidate/field constraint prevents duplicates.

The default starts one worker inside the web process. To run it separately, initialize
once and then launch these in separate terminals, from the same repository directory:

**PowerShell**

```powershell
uv run --frozen jobadder-autocoder --init-only
$env:AUTOCODER_EMBEDDED_WORKER = "0"
uv run --frozen jobadder-autocoder
# In the second terminal:
uv run --frozen jobadder-worker
```

**macOS / Linux**

```sh
uv run --frozen jobadder-autocoder --init-only
AUTOCODER_EMBEDDED_WORKER=0 uv run --frozen jobadder-autocoder
# In the second terminal:
uv run --frozen jobadder-worker
```

Both processes must use the same file-backed database. Settings:

| Variable | Default | Purpose |
|---|---|---|
| `AUTOCODER_DATABASE_URL` | `sqlite:///.data/autocoder.db` | Local persistent database |
| `AUTOCODER_EMBEDDED_WORKER` | `1` | `0` disables the web process's worker |
| `AUTOCODER_PAGE_SIZE` | `100` | Bounded pages, between 1 and 1,000 profiles |

This release deliberately accepts SQLite only. Postgres deployment needs its driver,
claim implementation, migrations, and concurrency checks before it can be enabled.
The abstraction boundaries prepare that transition; a configuration change alone is
not a validated multi-worker production deployment.

## API and checks

Interactive API documentation is at the local `/api/docs` path. `/api/health` checks
database access and identifies the synthetic mode. Every mutation needs a CSRF token:
make a GET request first, retain its `gt_csrf` cookie, and send that value in the
`X-CSRF-Token` header. HTML forms do this automatically. Cross-origin changes and
unrecognized Host headers are rejected.

```sh
uv run --frozen ruff check src tests
uv run --frozen ruff format --check src tests
uv run --frozen mypy src/jobadder_autocoder
uv run --frozen pytest
```

The tests cover read-only previews, field isolation, verbatim evidence, conflicting
sources, stale profiles, duplicate/concurrent approvals, atomic rollback, undo after
edits, retries, restart recovery, lease fencing, paging, HTML forms, and API behavior.
A 1,010-profile synthetic run checks bounded processing; it does **not** establish
accuracy on real CVs or throughput across the 200,000-profile JobAdder backlog.

## Before live JobAdder access

The next step is a **read-only** JobAdder adapter and gap sweep with OAuth and verified
pagination/rate-limit handling. Register the app, define the actual Country field and
picklist mapping, and use a test account or dummy candidates for integration tests.
Credentials must be entered securely outside Git; none are needed for this prototype.

Before enabling writes, verify partial-update semantics and conditional concurrency
controls. A fresh read followed by a write still has a race. The synthetic connector
uses an atomic version check; this guarantee must not be assumed for JobAdder.
Authentication, account boundaries, secure token storage, real-data retention, CV
parsing, LLM extraction, accuracy evaluation, and production deployment remain future work.
