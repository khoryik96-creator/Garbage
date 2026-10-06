# Garbage Truck · JobAdder auto-coder

A **Go core with a Python document/AI worker**, implementing the Country workflow
in [Claude's brief](IDEATION.md). Preview, review, persistent jobs, approvals, audit,
and guarded undo run in Go. Python owns the document-processing boundary.

This release uses **synthetic profiles only**. The document worker currently reads
plain text with deterministic residence rules. JobAdder OAuth, PDF/OCR, vision,
and LLM providers remain integration work; no credentials or paid model calls are needed.

## Install the app

Download a package from [Desktop preview 0.3.0](https://github.com/khoryik96-creator/Garbage/releases/tag/v0.3.0).

The 0.4.1 fixes are built by [Desktop installers](https://github.com/khoryik96-creator/Garbage/actions/workflows/desktop-release.yml).
Download the `desktop-windows` artifact for the reviewed 0.4.1 source revision to
use the new Setup.exe. The existing 0.3.0 tag is preserved. See
[0.4.1 review fixes](docs/reliability-0.4.1.md) and [0.4.0 changes](docs/reliability-0.4.0.md) for the checks and limits.

- **Windows 10/11, x64:** run `Garbage-Truck-0.3.0-windows-amd64-Setup.exe`, click
  Install, then open **Garbage Truck** from the Start menu or desktop shortcut.
- **macOS:** extract the ZIP for your processor, drag **Garbage Truck.app** to
  Applications, and open it.
- **Linux:** extract the TAR.GZ and run `sh install.sh`, or open the included executable directly.

The interface opens automatically in your default browser. The packaged Country
demo needs no Go, Python, or Docker installation. It runs locally, saves your work
automatically, and reopens the same workspace when you launch it again. Use
**Quit Garbage Truck** in the sidebar to stop the app. Upgrading and uninstalling
keep your saved data.

Windows builds support optional Authenticode signing; the package's Signing.json
reports the actual signature presence and Windows verification. Workspace settings
also checks the installed executable with Windows. macOS builds are not notarized.
Interactive JobAdder and AI connections remain integration work. See
[installation and backups](docs/installation.md) for details and the portable option.

## Run from source

Requirements: Go **1.27.1**, a C compiler for the embedded SQLite driver
(`CGO_ENABLED=1`), Python 3.12+, and [uv](https://docs.astral.sh/uv/).
The cloud instance already has GCC. Go embeds SQLite, templates, CSS, country data,
and field metadata in the compiled core; Python has no application database access.

In this cloud image, prefix `uv` commands with
`UV_CACHE_DIR=/tmp/garbage-uv-cache` because the default cache is read-only.
The saved cloud install and startup instructions already select that cache.

On Linux amd64, from this repository's root:

```sh
bash scripts/install-go.sh
bash scripts/go.sh build -o bin/garbage-truck ./cmd/garbage-truck
bash scripts/go.sh build -o bin/garbage-worker ./cmd/garbage-worker
uv sync --frozen
bin/garbage-truck
```

The app binds to loopback on port 8000. Add `--port 8001` to change it.
The helper installs a checksum-verified Go SDK in ignored `.tools/`; `scripts/go.sh`
uses it and writable caches. It uses native Git module downloads in this cloud
instance, where proxy archive downloads are blocked; module checksum verification
remains enabled. On other platforms, install Go 1.27.1 and a supported C compiler,
then use normal `go build` commands. Windows builds should use `.exe` output names.

The default Country demo works with the Go process alone. To exercise the Python
boundary, start these in separate terminals:

```sh
# Terminal 1
uv run --frozen garbage-document-worker --port 8002

# Terminal 2, macOS/Linux
AUTOCODER_DOCUMENT_WORKER_URL=http://127.0.0.1:8002 bin/garbage-truck
```

In PowerShell, set the variable before launching the core:

```powershell
$env:AUTOCODER_DOCUMENT_WORKER_URL = "http://127.0.0.1:8002"
.\bin\garbage-truck.exe
```

To build the desktop launcher on your own platform, use Go and a C compiler:

```sh
go build -o garbage-truck-desktop ./cmd/garbage-truck-desktop
./garbage-truck-desktop
```

In this cloud, use `bash scripts/go.sh build -o bin/garbage-truck-desktop ./cmd/garbage-truck-desktop`
and `bin/garbage-truck-desktop --no-browser --data-dir /tmp/garbage-desktop-demo`.
Desktop data uses the user's application-data folder; developer web runs retain
`.data/autocoder.db`. Pass `--data-dir` to the desktop host to select an existing
workspace folder. The installer builds are automated by `desktop-release.yml`.

Explicit address evidence stays in Go. Notes go through the optional Python worker.
Go validates response identity, protocol, country, and source quotes before storing
proposals. Both paths produce the same demo outcomes. Worker failures use durable,
bounded retries; document calls happen outside database transactions.

## Try the workflow

1. A fresh workspace has 10 profiles: 8 missing Country and 2 with existing values.
2. Start **Preview**: 6 proposals appear, including 2 requiring a human decision.
   Two other missing profiles have no permitted residence evidence. Preview changes no profiles.
3. Start **Review** and inspect each proposal's source quote after the scan completes.
4. Approve, reject, or correct a proposal with an explanation. Conflicts require a correction note.
5. Inspect **Profiles** and **Audit trail**. Use **Undo** on the run page to restore
   the original empty value while the profile remains unchanged since approval.

Country is the only enabled run field. `Unknown`, `N/A`, `TBC`, and `-` are preserved.
Nationality, phone origin, employment history, and city guesses are not residence
evidence. Pause, resume, and cancel retain committed progress.

Workspace settings provides **Download backup** and **Restore backup**. Backups
are consistent SQLite snapshots, including committed WAL pages. Restore validates
the database and keeps a `before-restore-*.db` recovery copy. Processing runs in a
restored backup are paused until you resume them. Failed restores keep or recover
the previous workspace. Uploaded backups are limited to 512 MiB.

After a crash, the installed app fences abandoned worker claims and resumes from
the last committed page with bounded retries. Run pages show processing, retry,
failure, and review status, and completed previews offer **Start a Review run**.

The standalone `cmd/garbage-jobadder-check` performs one bounded, read-only public
API request when a securely configured access token is available. See
[Kano and live prerequisites](docs/kano-reference.md). It does not import profiles,
enable live runs, or write to JobAdder.

## Modules and contract

```text
cmd/garbage-truck/       Go web process, with an optional embedded job worker
cmd/garbage-worker/      Independently runnable Go job worker
cmd/garbage-truck-desktop/ Installed app launcher
internal/application/   Shared app initialization and graceful shutdown
internal/desktop/       Per-user workspace, one running instance, browser opening
internal/domain/        Typed models, provider interfaces, versioned field catalogue
internal/policy/        Missing-field and evidence checks, country normalization
internal/pipeline/      Deterministic residence extraction
internal/connectors/    Synthetic adapter and Kano Country observation helper
internal/jobs/          Durable claims, leases, bounded pages, retries, checkpoints
internal/storage/       SQLite repositories, transactional schema migrations
internal/audit/         Approval, rejection, write records, guarded undo
internal/web/           HTTP API, server-rendered UI, embedded assets
internal/docworker/     Local Python-worker client and response validation
services/document_worker/  Python extraction service and tests
contracts/              Versioned document-worker JSON schema
packaging/              Windows installer and desktop package assets
```

The Python worker returns proposals; Go owns policy and all writes. The
[document contract](contracts/document-worker-v1.schema.json) carries a protocol
version, candidate ID, versioned source ID, selected field, text, and quoted evidence.
There are no write endpoints in Python. See [the architecture](docs/architecture.md),
[the Go decision](docs/adr/0002-go-core-python-document-worker.md), and
[the Kano reference](docs/kano-reference.md) for the integration boundaries.

## Persistence and separate Go workers

Startup migrations and seeding preserve existing records. The original Python
prototype's `.data/autocoder.db` is compatible: candidates, runs, jobs, proposals,
write-backs, and audit history are retained. The old Python core has been removed;
stop it before switching. Back up a valuable database before upgrading.

```sh
bin/garbage-truck --init-only
AUTOCODER_EMBEDDED_WORKER=0 bin/garbage-truck
# In another terminal, with the same database and document-worker setting:
bin/garbage-worker
```

| Variable | Default | Purpose |
|---|---|---|
| `AUTOCODER_DATABASE_PATH` | `.data/autocoder.db` | File-backed SQLite database |
| `AUTOCODER_DATABASE_URL` | unset | Legacy `sqlite:///...` compatibility; path setting takes precedence |
| `AUTOCODER_EMBEDDED_WORKER` | `1` | `0` runs web without an embedded worker |
| `AUTOCODER_PAGE_SIZE` | `100` | Bounded pages, between 1 and 1,000 profiles |
| `AUTOCODER_DOCUMENT_WORKER_URL` | unset | Optional local worker origin, normally port 8002 |

Keep database files, real CVs, and credentials out of Git. Live services are local
only. Authentication and account isolation are required before shared deployment.

## API and validation

The local `/api/docs` page links the OpenAPI schema. `/api/health` checks database
access; `/api/fields` exposes 20 Kano reference mappings. Browser SPA mappings remain
unverified for public API writes, and numeric custom IDs are account-specific.

For core mutations, retain the `gt_csrf` cookie from a GET and send its value in
`X-CSRF-Token`. Forms include it automatically. Cross-origin changes and unknown
Host headers are rejected. Bodies and pagination are bounded.

```sh
bash scripts/go.sh vet ./...
bash scripts/go.sh test -race -timeout 90s ./...
uv run --frozen ruff check services/document_worker
uv run --frozen ruff format --check services/document_worker
uv run --frozen mypy services/document_worker/src/garbage_document_worker
uv run --frozen pytest
python3 -m unittest discover -s scripts/tests -v
```

Checks cover workflow outcomes, field isolation, stale evidence, duplicate/concurrent
approval and undo across database connections, rollback, retry privacy, restart
recovery, renewed and abandoned leases, active-request cancellation, bounded
1,010-profile processing, audit pagination with tied timestamps, shared Go/Python
text rules, CSRF/forms, and untrusted document responses. They establish prototype
behavior, not real-CV accuracy or throughput over the 200,000-profile JobAdder account.

Live integration starts with official OAuth read-only access, regional API URLs,
pagination, throttling, and account field discovery. Verify partial-update and
conditional-write semantics before enabling writes. Postgres, remote-write
reconciliation, OCR/vision, and model accuracy evaluation follow the architecture plan.
