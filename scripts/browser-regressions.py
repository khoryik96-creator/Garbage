"""Exercise real navigation, short viewports, and workspace backup/restore."""

import argparse
import json
import os
import subprocess
import tempfile
import time
import urllib.request
from pathlib import Path

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
                page.get_by_role("button", name="Start Country run").click()
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
