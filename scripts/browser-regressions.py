"""Exercise real navigation, short viewports, and workspace backup/restore."""

import argparse
import base64
import json
import os
import re
import socket
import subprocess
import tempfile
import time
import urllib.request
from pathlib import Path
from urllib.parse import parse_qs, urlparse

from playwright.sync_api import expect, sync_playwright


def check(binary: Path, executable: str | None, artifacts: Path | None) -> None:
    with tempfile.TemporaryDirectory(prefix="Garbage browser ") as temp:
        directory = Path(temp)
        environment = dict(os.environ)
        environment.pop("AUTOCODER_DOCUMENT_WORKER_URL", None)
        process = subprocess.Popen(
            [str(binary.resolve()), "--no-browser", "--data-dir", str(directory)], env=environment
        )
        try:
            deadline = time.monotonic() + 20
            while True:
                if process.poll() is not None:
                    raise AssertionError("Desktop host exited during startup.")
                try:
                    info = json.loads((directory / "instance.json").read_text())
                    with urllib.request.urlopen(info["url"] + "api/desktop/instance", timeout=1):
                        base = info["url"]
                    break
                except (OSError, ValueError, KeyError):
                    if time.monotonic() > deadline:
                        raise AssertionError("Desktop startup timed out.") from None
                    time.sleep(0.05)
            with sync_playwright() as playwright:
                browser = playwright.chromium.launch(
                    executable_path=executable, args=["--no-sandbox"]
                )
                page = browser.new_page(viewport={"width": 1440, "height": 900})
                errors: list[str] = []
                page.on("pageerror", lambda error: errors.append(str(error)))
                page.goto(base + "profiles")
                search = page.locator("[data-profile-search]")
                search.fill("Alex Morgan")
                expect(page.locator("[data-profile-row]:visible")).to_have_count(1)
                page.get_by_role("link", name="Workspace settings").click()
                page.go_back()
                expect(search).to_have_value("Alex Morgan")
                expect(page.locator("[data-profile-row]:visible")).to_have_count(1)
                search.fill("a nonexistent person")
                page.get_by_role("link", name="Workspace settings").click()
                page.go_back()
                expect(page.locator("[data-profile-empty]")).to_be_visible()
                expect(page.locator("[data-profile-row]:visible")).to_have_count(0)
                print("Actual Back navigation restores filtering and the empty state.")

                for width, height in [(1440, 400), (844, 390), (390, 844), (1440, 900)]:
                    page.set_viewport_size({"width": width, "height": height})
                    page.goto(base)
                    settings = page.get_by_role("link", name="Workspace settings")
                    settings.focus()
                    expect(settings).to_be_in_viewport()
                    settings.click()
                    expect(page.get_by_role("heading", name="Workspace settings.")).to_be_visible()
                    quit_button = page.locator(".app-sidebar").get_by_role(
                        "button", name="Quit Garbage Truck"
                    )
                    quit_button.focus()
                    expect(quit_button).to_be_in_viewport()
                    assert quit_button.evaluate(
                        "el => { const r=el.getBoundingClientRect(); return "
                        "el.contains(document.elementFromPoint(r.x+r.width/2, r.y+r.height/2)); }"
                    ), "Quit is covered or cannot be reached."
                    print(f"Sidebar controls reachable at {width}×{height}.")

                page.set_viewport_size({"width": 1440, "height": 900})
                page.goto(base)
                page.get_by_role("button", name="Start selected-field run").click()
                expect(page.locator('[data-run-state="completed"]')).to_be_attached(timeout=15000)
                expect(page.get_by_role("button", name="Approve Country")).to_have_count(0)
                page.get_by_role("button", name="Start a Review run").click()
                expect(page.locator('[data-run-state="completed"]')).to_be_attached(timeout=15000)
                run_url = page.url
                alex = page.locator("article.suggestion").filter(has_text="Demo Alex Morgan")
                alex.get_by_role("button", name="Approve Country").click()
                expect(page.get_by_role("button", name="Undo", exact=True)).to_have_count(1)
                page.get_by_role("link", name="Workspace settings").click()
                with page.expect_download() as download:
                    page.get_by_role("button", name="Download backup").click()
                backup = directory / "downloaded-backup.db"
                download.value.save_as(backup)
                assert backup.read_bytes().startswith(b"SQLite format 3")
                page.goto(run_url)
                page.get_by_role("button", name="Undo", exact=True).click()
                expect(page.get_by_role("button", name="Undo", exact=True)).to_have_count(0)
                page.goto(base + "settings")
                page.get_by_label("Restore from a backup").set_input_files(backup)
                page.get_by_role("button", name="Restore backup", exact=True).click()
                expect(page.get_by_text("Backup restored.", exact=True)).to_be_visible()
                assert list(directory.glob("before-restore-*.db"))
                page.goto(run_url)
                expect(page.get_by_role("button", name="Undo", exact=True)).to_have_count(1)
                if artifacts:
                    artifacts.mkdir(parents=True, exist_ok=True)
                    page.screenshot(path=str(artifacts / "review.png"), full_page=True)
                page.goto(base)
                for field in ["email", "mobile", "current_employer"]:
                    page.locator(f'input[name="fields"][value="{field}"]').check()
                page.get_by_label("Run mode").select_option("review")
                page.get_by_role("button", name="Start selected-field run").click()
                expect(page.locator('[data-run-state="completed"]')).to_be_attached(timeout=15000)
                alex_mobile = (
                    page.locator("article.suggestion")
                    .filter(has_text="Demo Alex Morgan")
                    .filter(has=page.get_by_role("button", name="Approve Mobile"))
                )
                expect(alex_mobile).to_have_count(1)
                expect(
                    page.locator("article.suggestion")
                    .filter(has_text="Demo Alex Morgan")
                    .filter(has=page.get_by_role("button", name="Approve Email"))
                ).to_have_count(0)
                alex_mobile.get_by_role("button", name="Approve Mobile").click()
                expect(page.get_by_role("button", name="Undo", exact=True)).to_have_count(1)
                page.get_by_role("button", name="Undo", exact=True).click()
                page.goto(base + "settings")
                expect(page.get_by_label("Client ID", exact=True)).to_be_visible()
                expect(page.get_by_label("Client Secret", exact=True)).to_have_attribute(
                    "type", "password"
                )
                page.get_by_label("Client ID", exact=True).fill("invalid-test-client")
                page.get_by_label("Client Secret", exact=True).fill("test-secret-must-never-return")
                page.get_by_label("Registered callback URL").fill(
                    "https://example.invalid/callback"
                )
                page.get_by_role("button", name="Connect JobAdder", exact=True).click()
                assert "test-secret-must-never-return" not in page.content()
                page.goto(base + "settings")
                expect(page.get_by_label("Client Secret", exact=True)).to_have_value("")
                with socket.socket() as occupied:
                    occupied.bind(("127.0.0.1", 0))
                    occupied.listen(1)
                    page.get_by_label("Client ID", exact=True).fill("test-client")
                    page.get_by_label("Client Secret", exact=True).fill(
                        "test-secret-must-never-return"
                    )
                    page.get_by_label("Registered callback URL").fill(
                        f"http://127.0.0.1:{occupied.getsockname()[1]}/jobadder/callback"
                    )
                    page.get_by_label(
                        "Remember this connection securely on this computer"
                    ).uncheck()
                    page.get_by_role("button", name="Connect JobAdder", exact=True).click()
                    expect(
                        page.get_by_text(re.compile("The callback port is in use"))
                    ).to_be_visible()
                    assert "test-secret-must-never-return" not in page.content()
                    page.get_by_role("link", name="Return to Settings", exact=True).click()
                    expect(page.get_by_label("Client ID", exact=True)).to_have_value("")
                with socket.socket() as callback_port:
                    callback_port.bind(("127.0.0.1", 0))
                    callback = (
                        f"http://127.0.0.1:{callback_port.getsockname()[1]}/jobadder/callback"
                    )
                # Chromium 153 does not apply Playwright URL routing to every
                # redirected form request. Fetch interception covers the actual
                # identity navigation without making an external network call.
                identity = page.context.new_cdp_session(page)
                identity.send(
                    "Fetch.enable",
                    {
                        "patterns": [
                            {"urlPattern": "https://id.jobadder.com/*", "requestStage": "Request"}
                        ]
                    },
                )
                intercepted: list[str] = []

                def sign_in_fixture(event: dict) -> None:
                    intercepted.append(event["request"]["url"])
                    identity.send(
                        "Fetch.fulfillRequest",
                        {
                            "requestId": event["requestId"],
                            "responseCode": 200,
                            "responseHeaders": [{"name": "Content-Type", "value": "text/html"}],
                            "body": base64.b64encode(b"<h1>JobAdder sign-in fixture</h1>").decode(),
                        },
                    )

                identity.on("Fetch.requestPaused", sign_in_fixture)
                page.get_by_label("Client ID", exact=True).fill("test-client")
                page.get_by_label("Client Secret", exact=True).fill("test-secret-must-never-return")
                page.get_by_label("Registered callback URL").fill(callback)
                page.get_by_label("Remember this connection securely on this computer").uncheck()
                page.get_by_role("button", name="Connect JobAdder", exact=True).click()
                expect(page).to_have_url(
                    re.compile(r"https://id\.jobadder\.com/connect/authorize\?.*")
                )
                expect(page.get_by_role("heading", name="JobAdder sign-in fixture")).to_be_visible()
                assert (
                    sum(
                        url.startswith("https://id.jobadder.com/connect/authorize?")
                        for url in intercepted
                    )
                    == 1
                )
                assert "test-secret-must-never-return" not in page.url
                state = parse_qs(urlparse(page.url).query)["state"][0]
                identity.send("Fetch.disable")
                page.goto(callback + "?state=" + state + "&error=access_denied")
                expect(page).to_have_url(base + "settings?jobadder=failed")
                expect(
                    page.get_by_text(re.compile("JobAdder sign-in was cancelled or denied"))
                ).to_be_visible()
                identity.send(
                    "Fetch.enable",
                    {
                        "patterns": [
                            {"urlPattern": "https://id.jobadder.com/*", "requestStage": "Request"}
                        ]
                    },
                )
                page.get_by_role("button", name="Connect JobAdder", exact=True).click()
                expect(page.get_by_role("heading", name="JobAdder sign-in fixture")).to_be_visible()
                identity.send("Fetch.disable")
                identity.detach()
                page.goto(base + "settings")
                expect(page.get_by_label("Client Secret", exact=True)).to_have_value("")
                expect(page.get_by_text(re.compile("Waiting for JobAdder sign-in"))).to_be_visible()
                page.get_by_role("button", name="Cancel sign-in", exact=True).click()
                expect(page.get_by_text(re.compile("Waiting for JobAdder sign-in"))).to_have_count(
                    0
                )
                page.get_by_role("button", name="Disconnect and remove saved details").click()
                if artifacts:
                    page.screenshot(path=str(artifacts / "settings.png"), full_page=True)
                print(
                    "Multiple field selection, protected email, mobile approval/undo "
                    "and OAuth settings, occupied port, denied sign-in, retry and cancel passed."
                )
                assert not errors, errors
                page.locator(".app-sidebar").get_by_role(
                    "button", name="Quit Garbage Truck"
                ).click()
                expect(page.get_by_text("Your work is saved.", exact=True)).to_be_visible()
                assert process.wait(timeout=10) == 0
                browser.close()
                print("Preview, approval, undo, backup, restore, and quit passed in Chromium.")
        finally:
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=20)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=5)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--browser-executable")
    parser.add_argument("--artifacts", type=Path)
    args = parser.parse_args()
    check(args.binary, args.browser_executable, args.artifacts)


if __name__ == "__main__":
    main()
