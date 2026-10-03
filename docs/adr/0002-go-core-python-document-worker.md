# Decision 0002: Go core, Python document worker

Status: accepted. Supersedes Decision 0001's language and framework selection.

The user chose Go as the long-term core while retaining Python for document and AI
processing. The first prototype's behavior is the acceptance baseline: gap reporting,
preview/review, missing-only Country writes, audit, guarded undo, and durable jobs.

The core uses Go's HTTP server, HTML templates, domain interfaces, explicit SQL
repositories, and an embedded SQLite driver. This driver needs CGO and a C compiler
at build time. Built core binaries include their UI and reference assets; platform
builds remain distinct. The cloud SDK is pinned and checksum verified.

Python exposes a versioned local extraction contract and owns no core data or write
credentials. Deterministic text extraction exercises that boundary today. Model,
PDF/OCR, and vision adapters can evolve within it after policy and accuracy testing.
Go validates proposals and owns all write decisions. Network extraction runs outside
SQL transactions, then a lease check fences the final atomic page commit.

This keeps a modular monolith for the application rather than distributing every
module. Operating the optional Python service adds deployment work; hosted-model
HTTP calls alone do not require Python. SQLite history is retained using compatible
tables and Go migrations, with cross-connection concurrency and recovery tests.

JobAdder integration, account throttling, production authorization, Postgres,
remote-write reconciliation, and document/model evaluation remain planned work.
