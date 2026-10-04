"""Kill a host during extraction and verify recovery from a committed page."""

import argparse
import http.cookiejar
import json
import os
import subprocess
import tempfile
import threading
import time
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path


def check(binary: Path) -> None:
    pending, release = threading.Event(), threading.Event()

    class DocumentHandler(BaseHTTPRequestHandler):
        def log_message(self, *_: object) -> None:
            pass

        def do_POST(self) -> None:
            request = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            if request["candidate_id"] == 1004:
                pending.set()
                release.wait(20)
                return
            result = {
                "protocol_version": 1,
                "candidate_id": request["candidate_id"],
                "source_id": request["source_id"],
                "status": "proposed",
                "extraction": {
                    "value": "NZ",
                    "confidence": "high",
                    "evidence": [{"source": "notes", "quote": request["text"]}],
                    "reason": "Explicit country of residence.",
                },
            }
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps(result).encode())

    service = ThreadingHTTPServer(("127.0.0.1", 0), DocumentHandler)
    thread = threading.Thread(target=service.serve_forever, daemon=True)
    thread.start()
    try:
        with tempfile.TemporaryDirectory(prefix="Garbage crash ") as temp:
            directory = Path(temp)
            environment = dict(
                os.environ,
                AUTOCODER_PAGE_SIZE="3",
                AUTOCODER_DOCUMENT_WORKER_URL=f"http://127.0.0.1:{service.server_port}",
            )
            process = subprocess.Popen(
                [str(binary.resolve()), "--no-browser", "--data-dir", temp], env=environment
            )

            def ready() -> str:
                deadline = time.monotonic() + 15
                while time.monotonic() < deadline:
                    try:
                        info = json.loads((directory / "instance.json").read_text())
                        with urllib.request.urlopen(
                            info["url"] + "api/desktop/instance", timeout=1
                        ):
                            return str(info["url"])
                    except (OSError, ValueError, KeyError):
                        time.sleep(0.05)
                raise AssertionError("Host did not start after interruption.")

            try:
                base = ready()
                jar = http.cookiejar.CookieJar()
                opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
                opener.open(base + "settings", timeout=3).close()
                token = next(cookie.value for cookie in jar if cookie.name == "gt_csrf")
                request = urllib.request.Request(
                    base + "api/runs",
                    data=b'{"mode":"review","fields":["country"]}',
                    headers={"Content-Type": "application/json", "X-CSRF-Token": token},
                )
                with opener.open(request, timeout=3) as response:
                    run = json.load(response)
                assert pending.wait(10), "Worker did not reach its second page."
                with opener.open(base + "api/runs/" + run["id"], timeout=3) as response:
                    assert json.load(response)["processed"] == 3
                process.kill()
                process.wait(timeout=5)
                environment.pop("AUTOCODER_DOCUMENT_WORKER_URL")
                process = subprocess.Popen(
                    [str(binary.resolve()), "--no-browser", "--data-dir", temp], env=environment
                )
                base = ready()
                deadline = time.monotonic() + 15
                while time.monotonic() < deadline:
                    with opener.open(base + "api/runs/" + run["id"], timeout=3) as response:
                        current = json.load(response)
                    if current["state"] == "completed":
                        break
                    time.sleep(0.05)
                assert current["state"] == "completed"
                assert current["processed"] == 10 and current["proposed"] == 6
                with opener.open(
                    base + "api/runs/" + run["id"] + "/suggestions", timeout=3
                ) as response:
                    assert len(json.load(response)["items"]) == 6
                with opener.open(base + "audit", timeout=3) as response:
                    assert "Run Recovered" in response.read().decode()
                print("Crash recovery kept the saved checkpoint without duplicate proposals.")
            finally:
                if process.poll() is None:
                    process.terminate()
                    process.wait(timeout=20)
    finally:
        release.set()
        service.shutdown()
        service.server_close()
        thread.join(timeout=3)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, type=Path)
    check(parser.parse_args().binary)
