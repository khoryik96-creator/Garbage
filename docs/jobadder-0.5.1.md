# Garbage Truck 0.5.1: connection fixes

Install the newer Windows Setup.exe after quitting Garbage Truck. Upgrades keep
the existing workspace. Open Workspace settings, enter your registered JobAdder
OAuth app details locally, and click Connect JobAdder. The registered callback
must exactly match the URL shown in Settings (by default
`http://127.0.0.1:8765/jobadder/callback`). Finish sign-in on JobAdder's page.

Settings now shows pending sign-in and offers Cancel sign-in. Denied sign-in
returns to Settings with a retry message. An occupied callback port or unavailable
protected storage leaves a previously working connection intact. Reconnecting
with the same app retains the previous token until the new sign-in succeeds.
Cancelling during a slow token exchange cancels the request and prevents even a
late response from saving new credentials. Invalid or unbound callbacks cannot
consume a valid attempt. Remembered credentials remain protected by Windows DPAPI.

Connected accounts have a JobAdder profiles link in the sidebar. Live candidate
summaries show returned email, mobile, phone, state and country code. A returned
country code also supplies the country display when the name is absent. A value
not returned by the list API is shown as **Not returned**, distinct from an
explicitly empty value. Employment and notes require candidate detail reads;
they are not assessed from summaries. Live pages identify their JobAdder source.

JobAdder browsing remains read-only; filling runs still use the separate demo
workspace and preserve populated fields. Account field mappings, picklists and
safe concurrent writes require verification before live editing can be enabled.
No real account credentials are configured in the test environment. Controlled
provider responses verify the complete HTTP flow, token exchange, regional
pagination, persistence and disconnect. Chromium verifies the actual sign-in
navigation, occupied ports, denied callbacks, retry and cancellation. This does
not establish that the user's OAuth app registration or account permissions work.

The installer workflow runs native Windows DPAPI, install, upgrade, uninstall,
reinstall, interrupted-upgrade recovery and saved-data checks, and verifies the
macOS binary's minimum version against Info.plist. Signing reports describe the
actual package; builds without a signing certificate remain unsigned. The
existing v0.3.0 tag and earlier packages are preserved.
