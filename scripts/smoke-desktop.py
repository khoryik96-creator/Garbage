"""Exercise a packaged desktop host, including reopening and saved-work recovery."""

import argparse
import http.cookiejar
import json
import os
import subprocess
import tempfile
import time
import urllib.request
from pathlib import Path


def smoke(binary: Path, directory: Path, expected_run: str | None = None) -> dict[str, str]:
    directory.mkdir(parents=True, exist_ok=True)
    environment = dict(os.environ)
    environment.pop("AUTOCODER_DOCUMENT_WORKER_URL", None)
    processes: list[subprocess.Popen[bytes]] = []

    def launch() -> subprocess.Popen[bytes]:
        process = subprocess.Popen(
            [str(binary.resolve()), "--no-browser", "--data-dir", str(directory.resolve())],
            env=environment,
        )
        processes.append(process)
        return process

    def ready(process: subprocess.Popen[bytes]) -> str:
        for _ in range(100):
            if process.poll() is not None:
                raise AssertionError("Desktop host exited during startup.")
            try:
                info = json.loads((directory / "instance.json").read_text())
                with urllib.request.urlopen(info["url"] + "api/desktop/instance", timeout=1) as r:
                    if json.load(r)["instance_id"] == info["instance_id"]:
                        return str(info["url"])
            except (OSError, ValueError, KeyError):
                pass
            time.sleep(0.1)
        raise AssertionError("Desktop host did not become ready.")

    jar = http.cookiejar.CookieJar()
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))

    def quit_app(base: str, process: subprocess.Popen[bytes]) -> None:
        opener.open(base + "settings", timeout=3).close()
        csrf = next(c.value for c in jar if c.name == "gt_csrf")
        request = urllib.request.Request(
            base + "desktop/quit",
            data=f"_csrf={csrf}".encode(),
            headers={"Content-Type": "application/x-www-form-urlencoded"},
        )
        with opener.open(request, timeout=3) as response:
            assert "Your work is saved." in response.read().decode()
        assert process.wait(timeout=5) == 0
        assert not (directory / "instance.json").exists()

    try:
        process = launch()
        base = ready(process)
        if expected_run:
            with opener.open(base + "api/runs/" + expected_run, timeout=3) as response:
                assert json.load(response)["id"] == expected_run
        opener.open(base + "settings", timeout=3).close()
        csrf = next(c.value for c in jar if c.name == "gt_csrf")
        request = urllib.request.Request(
            base + "api/runs",
            data=b'{"mode":"review","fields":["country"]}',
            headers={"Content-Type": "application/json", "X-CSRF-Token": csrf},
        )
        with opener.open(request, timeout=3) as response:
            run = json.load(response)
        for _ in range(100):
            with opener.open(base + "api/runs/" + run["id"], timeout=3) as response:
                current = json.load(response)
            if current["state"] == "completed":
                break
            time.sleep(0.1)
        assert current["state"] == "completed"
        assert current["processed"] == 10 and current["proposed"] == 6
        with opener.open(base + "api/runs/" + run["id"] + "/suggestions", timeout=3) as response:
            suggestion = next(
                item for item in json.load(response)["items"] if item["candidate_id"] == 1001
            )
        request = urllib.request.Request(
            base + "api/suggestions/" + suggestion["id"] + "/approve",
            data=b"{}",
            headers={"Content-Type": "application/json", "X-CSRF-Token": csrf},
        )
        with opener.open(request, timeout=3) as response:
            write_id = json.load(response)["writeback_id"]
        reopened = launch()
        assert reopened.wait(timeout=10) == 0 and process.poll() is None
        assert json.loads((directory / "instance.json").read_text())["url"] == base
        quit_app(base, process)
        process = launch()
        base = ready(process)
        with opener.open(base + "api/runs/" + run["id"], timeout=3) as response:
            assert json.load(response)["proposed"] == 6
        with opener.open(base + "api/runs/" + run["id"] + "/writebacks", timeout=3) as response:
            assert any(
                item["id"] == write_id and item["state"] == "applied"
                for item in json.load(response)
            )
        opener.open(base + "settings", timeout=3).close()
        csrf = next(c.value for c in jar if c.name == "gt_csrf")
        request = urllib.request.Request(
            base + "api/writebacks/" + write_id + "/undo",
            data=b"{}",
            headers={"X-CSRF-Token": csrf},
        )
        with opener.open(request, timeout=3) as response:
            assert json.load(response)["state"] == "undone"
        quit_app(base, process)
        assert (directory / "autocoder.db").is_file()
        print("Packaged desktop launch, Country run, repeated launch, quit, and restart passed.")
        return {"run_id": run["id"], "writeback_id": write_id}
    finally:
        for process in reversed(processes):
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=5)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, type=Path)
    parser.add_argument("--data-dir", type=Path)
    parser.add_argument("--expect-run")
    parser.add_argument("--report", type=Path)
    args = parser.parse_args()
    if args.data_dir:
        result = smoke(args.binary, args.data_dir, args.expect_run)
    else:
        with tempfile.TemporaryDirectory(prefix="Garbage Truck smoke ") as temp:
            result = smoke(args.binary, Path(temp), args.expect_run)
    if args.report:
        args.report.write_text(json.dumps(result, indent=2) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
