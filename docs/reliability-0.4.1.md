# Garbage Truck 0.4.1 review fixes

This revision resolves the six findings from the independent 0.4.0 review.

- Restore validates typed candidate, run, suggestion, writeback, job, and audit
  records before replacing a workspace. Invalid uploads retain readable profiles,
  audit history, and prior data.
- Reopening uses per-instance, origin-restricted desktop IPC so the running owner
  observes browser opener failures after the second launcher exits. If the owner
  exits between readiness and IPC, lock acquisition continues.
- Windows installation extracts into a separate folder before replacement, keeps
  a complete recovery executable, and atomically replaces the executable on the
  same volume. Interrupted setup is recovered on retry, and shortcut working
  directories refer to the installed folder.
- OAuth has an explicit confidential-client compatibility mode for Kano's
  tested provider contract; PKCE and state validation remain enabled by default.
- Pagination rejects resolved relative self-loops.
- Rate-limit errors preserve full Retry-After delays and avoid retrying before
  the provider's delay when it exceeds the operation budget.

Regression tests exercise malformed uploads through HTTP, delayed opener failure
across two real processes, shutdown between readiness and reopen IPC, both OAuth
modes, relative pagination loops, and long numeric/date rate-limit delays.
Native Windows checks inject actual NSIS extraction failure and terminate setup
before/after replacement, then retry and check executable/data hashes. They also
exercise legacy leftover backup recovery and verify shortcut working directories.

Windows signing remains optional; absent a certificate, packages report unsigned.
Live JobAdder account connection and writes, certificate-backed signing, and
manual minimum-OS validation retain the prerequisites documented for 0.4.0.
