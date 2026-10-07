# Garbage Truck 0.5.0: fields and JobAdder connection

The run form now offers Country, Email, Mobile, Phone, LinkedIn, Current position,
Current employer, Notice period, State and Work type. Select any combination.
The extra fields use explicit labeled lines in profile notes; Country retains its
residence evidence rules. Account-specific fields and composite salary fields
remain disabled until their contracts are verified.

Every run fills blanks only. Populated values, including Unknown, N/A, TBC and
legacy job titles, are protected. Approval checks the current record and source
again, and unexpected changes roll back the whole transaction. Each approved
field is identified in history. Multiple app-approved fields can be undone while
an outside revision change still blocks earlier undo operations.

## Connect your JobAdder app

1. Open Workspace settings in the installed application.
2. Enter your OAuth app Client ID and Client Secret locally. Do not share them in
   chat or enter your normal JobAdder password into this form.
3. Register the exact callback URL in JobAdder. The default is
   `http://127.0.0.1:8765/jobadder/callback`. Use the same loopback hostname as the
   application, and an available port. This desktop flow requires a registered
   loopback callback; an HTTPS-only redirect registration requires a different,
   externally hosted callback integration.
4. Enable PKCE only if your registered app supports it. The confidential client
   flow, also used by Kano, is the default. There is no automatic downgrade.
5. Choose whether to remember credentials, then click Connect JobAdder and sign
   in on JobAdder's own page. Requested scopes are read, read_candidate and
   offline_access. Connected accounts expose Browse JobAdder profiles.

Windows stores remembered credentials and tokens in a DPAPI-encrypted file for
the current Windows user. macOS uses the native Keychain API; Linux requires an
unlocked Secret Service keyring and secret-tool. Storage failure is explicit;
session-only mode is available. Secrets are never rendered back into the form,
placed in browser storage or included in SQLite backups. Disconnect removes saved
details. Rotated refresh tokens and the OAuth regional API are retained.

The callback checks a random state, an expiring flow and the initiating browser's
cookie. The sign-in form's browser policy permits only the local application and
JobAdder's identity origin. Public API reads validate regional hosts and pagination
before sending a token.

## Scope and compatibility

Live JobAdder access is read-only. Selected-field runs still operate on the local
demo workspace. Public write schemas, account picklists and concurrency guarantees
have not been verified; browser SPA whole-record writes from Kano are not used.
No live account credentials are configured in the build environment. Sign-in is
tested against controlled provider responses, not the user's account.

Schema version 2 migrates existing version 1 workspaces in place and retains
profiles, approvals, audit and undo history. Restoring a version 1 backup migrates
a staging copy and leaves the original backup unchanged. Older application
versions cannot open a version 2 workspace; retain a pre-upgrade backup when a
downgrade may be needed. The v0.3.0 release tag remains preserved.

Regression coverage includes multi-field selection, populated-field protection,
title aliases, late edits, independent undo guards, backup/restore and legacy
history, browser Back navigation, short-window controls, real browser OAuth
redirects, token refresh, regional API retention, disconnect and native Windows
DPAPI. Native desktop CI verifies Windows install/upgrade/uninstall and recovery,
and matches the macOS binary's deployment target with bundle metadata.

Signing support is unchanged: packages accurately report their actual signatures.
Without a configured signing certificate, Windows installers remain unsigned.
