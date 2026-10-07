# Desktop reliability and workspace recovery · 0.4.0

The changes start from `a51b947`; fetching main found no newer commits. The
existing `v0.3.0` tag is preserved. New package filenames use 0.4.0.

| Finding | Change | Regression evidence |
| --- | --- | --- |
| Relaunch during shutdown | Retry the workspace lock while the old host drains requests; retain metadata through HTTP/worker/database shutdown | Authenticated pending-body request, overlapping relaunch, and saved run history |
| Hidden browser failures | Observe Unix opener exit failures, log a usable URL, and use Windows ShellExecute errors directly | Failing and long-running helper processes; native Windows host checks |
| Unreachable sidebar | Scroll the fixed sidebar and keep controls from shrinking away | Chromium at 1440×400, 844×390, 390×844, and 1440×900 |
| Back loses filtering | Apply initial/pageshow filters and persist the query in the history URL | Actual Chromium Back navigation with matching and empty results |
| Linux path escaping | Apply both desktop-entry escape layers; use fixed `/bin/sh` argv[0] to allow percent paths in GIO | Actual GIO launches with $, quotes, backticks, %, backslashes, and combined paths |
| macOS target mismatch | Compile for 13.0, declare 13.0 in Info.plist, and inspect every Mach-O slice before packaging | Reject wrong architectures, malformed binaries, and 12/26 targets; native package Compatibility.json |

Workspace settings now offers consistent online SQLite backups and validated
restore. Restore checks integrity, supported schema/version, references, and JSON
record shapes, keeps a recovery snapshot, and uses SQLite's transactional backup
API to replace the open database. Restored active runs are paused and outstanding
claims are revoked. Tests preserve approvals, audit history, and guarded undo;
invalid uploads and cancelled backups cannot replace current data.

The desktop host reclaims interrupted leases only while holding its exclusive
workspace lock. Other hosts retain expiry-based recovery. Committed pages remain
intact, obsolete tokens cannot commit, and retries stay bounded. The real-process
crash test kills the host during its second extraction page, restarts, and checks
the final counters, proposals, and recovery audit.

Run pages show processing/retry/failure actions and the number awaiting review.
Completed previews offer a fresh Review scan. Windows gains an interface-reopen
shortcut, update instructions, OS checks, and recovery of the previous executable
if replacement fails. Native installation checks upgrade from the retained 0.3.0
installer, compare the real SQLite database across upgrade/uninstall/reinstall,
and reopen prior run history.

Optional Authenticode signing covers the application, installer, and uninstaller.
Configure `WINDOWS_SIGNING_PFX_BASE64` and `WINDOWS_SIGNING_PFX_PASSWORD` as release
workflow secrets, or use an existing certificate through
`GARBAGE_SIGNING_CERT_THUMBPRINT` with `scripts/sign-windows.ps1`. Passwords are not
passed in command arguments. Signing must pass Windows verification. Signing.json
reports signature presence and verification for the exact files; an unsigned
build is never labelled verified. Runtime settings uses WinVerifyTrust, with
cache-only retrieval, and reports when a signature cannot be verified.

## Validation

Linux local checks pass: Go race tests, Python worker checks, packaging tests
including real GIO, Chromium workflow/navigation/layout checks, desktop smoke
checks, and real crash recovery. A CGO-enabled Windows executable and NSIS 0.4.0
installer were produced locally; they are unsigned.

Native Windows installation and macOS binary inspection are required by the
Desktop installers workflow on the draft PR. Their observed results must be
recorded before claiming platform validation. Signing cannot be exercised without
a configured certificate. macOS signing/notarization is not implemented.

Live JobAdder connection remains pending registered OAuth credentials, secure
desktop token persistence, account schemas/picklists, and verified write
semantics. See [the inspected Kano reference](kano-reference.md).
