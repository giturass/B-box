"""Run UI and native regressions independently on a CI emulator."""

import functools
import base64
import contextlib
import http.server
import json
import os
import re
import subprocess
import ssl
import tempfile
import threading
import time
import traceback
import urllib.parse
import xml.etree.ElementTree as ET
from pathlib import Path

PACKAGE = "io.ericlee.bfa"
COMPONENT = PACKAGE + "/io.nekohasekai.sfa.compose.MainActivity"
OUT = Path("build/android-smoke")


def adb(*args, binary=False, timeout=45):
    return subprocess.check_output(["adb", *args], text=not binary, timeout=timeout)


def nodes():
    for _ in range(4):
        try:
            result = adb("shell", "uiautomator", "dump", "/sdcard/box-window.xml")
            if "dumped" not in result.lower():
                time.sleep(0.5)
                continue
            document = adb("shell", "cat", "/sdcard/box-window.xml")
            (OUT / "window.xml").write_text(document)
            return list(ET.fromstring(document).iter("node"))
        except (subprocess.SubprocessError, ET.ParseError):
            time.sleep(0.5)
    return []


def matching(label, items):
    return [node for node in items if label in (
        node.get("text", "") + "\n" + node.get("content-desc", "")
    ).splitlines()]


def wait_for(label, timeout=90, dismiss_launcher_anr=False):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        current = nodes()
        found = matching(label, current)
        if found:
            return found
        # Google APIs images can start with a Pixel Launcher ANR over our app.
        # Dismiss only that known emulator failure, once; B-box ANRs still fail.
        if dismiss_launcher_anr and matching("Pixel Launcher isn't responding", current):
            close = [node for node in current if node.get("resource-id") == "android:id/aerr_close"]
            if close:
                dismiss_launcher_anr = False
                capture("launcher-anr")
                tap_node(close[0])
                adb("shell", "am", "start", "-W", "-n", COMPONENT)
        time.sleep(0.5)
    raise AssertionError(f"UI never showed {label!r}; see window.xml")


def tap_node(item):
    x1, y1, x2, y2 = map(int, re.findall(r"\d+", item.attrib["bounds"]))
    adb("shell", "input", "tap", str((x1 + x2) // 2), str((y1 + y2) // 2))
    time.sleep(0.6)


def click(label):
    found = wait_for(label)
    exact = [node for node in found if node.get("text") == label or node.get("content-desc") == label]
    if label in ("Start", "Stop"):
        assert exact, "The VPN button must have its own accessible tap target"
    tap_node((exact or found)[-1])


def capture(name):
    (OUT / f"{name}.png").write_bytes(adb("exec-out", "screencap", "-p", binary=True))


def ui_smoke():
    config = {
        "log": {"level": "info"},
        "inbounds": [{"type": "tun", "address": ["172.19.0.1/30"], "auto_route": True}],
        "outbounds": [
            {"type": "selector", "tag": "Smoke selector", "outbounds": ["direct"]},
            {"type": "direct", "tag": "direct"},
        ],
        "route": {"final": "Smoke selector"},
        "experimental": {"clash_api": {"default_mode": "rule"}},
    }
    (OUT / "profile.json").write_text(json.dumps(config))
    handler = functools.partial(http.server.SimpleHTTPRequestHandler, directory=str(OUT))
    with http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler) as server:
        port = server.server_port
        threading.Thread(target=server.serve_forever, daemon=True).start()
        try:
            adb("reverse", f"tcp:{port}", f"tcp:{port}")
            adb("shell", "am", "start", "-W", "-n", COMPONENT)
            wait_for("Dashboard", dismiss_launcher_anr=True)
            capture("01-startup")
            uri = "sing-box://import-remote-profile?" + urllib.parse.urlencode({
                "url": f"http://127.0.0.1:{port}/profile.json",
            }) + "#" + urllib.parse.quote("Smoke profile")
            # ADB's shell parses the encoded URI a second time.
            adb("shell", "am", "start", "-W", "-n", COMPONENT,
                "-a", "android.intent.action.VIEW", "-d", "'" + uri + "'")
            click("Save")
            wait_for("Smoke profile")
            capture("02-imported")
            click("Start")
            for _ in range(12):
                current = nodes()
                if matching("Running", current):
                    break
                for label in ("Allow", "OK"):
                    if matching(label, current):
                        tap_node(matching(label, current)[-1])
                        break
                time.sleep(0.5)
            wait_for("Running")
            capture("03-vpn-running")
            click("Proxies")
            wait_for("Smoke selector")
            capture("04-core-proxies")
            click("Dashboard")
            click("Stop")
            wait_for("Not running")
            adb("shell", "am", "force-stop", PACKAGE)
            adb("shell", "am", "start", "-W", "-n", COMPONENT)
            wait_for("Smoke profile")
            capture("05-restored-profile")
        finally:
            server.shutdown()
            try:
                adb("reverse", "--remove", f"tcp:{port}")
            except (subprocess.SubprocessError, OSError) as error:
                print(f"Reverse-port cleanup failed: {error}")


@contextlib.contextmanager
def mitm_origin():
    """Independent TLS origin for the device's actual libbox/Keystore path.

    Keys are generated only on the CI runner, outside uploaded artifacts, and
    removed when the suite ends. Only the public root is passed to the device.
    """
    scratch = Path.home() / "tmp"
    scratch.mkdir(exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="bbox-mitm-origin-", dir=scratch) as directory:
        root = Path(directory)
        commands = [
            ["openssl", "req", "-x509", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:P-256", "-nodes",
             "-sha256", "-days", "1", "-subj", "/CN=B-box independent CI origin root", "-keyout", "root.key", "-out", "root.pem",
             "-addext", "basicConstraints=critical,CA:TRUE", "-addext", "keyUsage=critical,keyCertSign,cRLSign"],
            ["openssl", "req", "-new", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:P-256", "-nodes",
             "-subj", "/CN=origin.test", "-keyout", "origin.key", "-out", "origin.csr"],
            ["openssl", "x509", "-req", "-in", "origin.csr", "-CA", "root.pem", "-CAkey", "root.key", "-set_serial", "2",
             "-days", "1", "-sha256", "-extfile", "extensions.txt", "-out", "origin.pem"],
        ]
        (root / "extensions.txt").write_text("basicConstraints=critical,CA:FALSE\nkeyUsage=critical,digitalSignature\n"
                                             "extendedKeyUsage=serverAuth\nsubjectAltName=DNS:origin.test\n")
        for command in commands:
            subprocess.run(command, cwd=root, check=True, capture_output=True, timeout=30)
        requests = []

        class Origin(http.server.BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"

            def do_GET(self):
                requests.append({"path": self.path, "host": self.headers.get("Host")})
                body = b'{"mitm":"through-original-route"}'
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.send_header("Set-Cookie", "first=1")
                self.send_header("Set-Cookie", "second=2")
                self.send_header("Connection", "close")
                self.end_headers()
                self.wfile.write(body)

        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.minimum_version = ssl.TLSVersion.TLSv1_2
        context.set_alpn_protocols(["http/1.1"])
        context.load_cert_chain(root / "origin.pem", root / "origin.key")
        with http.server.ThreadingHTTPServer(("127.0.0.1", 0), Origin) as server:
            port = server.server_port
            server.socket = context.wrap_socket(server.socket, server_side=True)
            threading.Thread(target=server.serve_forever, daemon=True).start()
            try:
                adb("reverse", f"tcp:{port}", f"tcp:{port}")
                yield ["-e", "mitmOriginPort", str(port), "-e", "mitmOriginCA",
                       base64.b64encode((root / "root.pem").read_bytes()).decode("ascii")]
                assert requests == [{"path": "/probe?raw=1;2", "host": f"origin.test:{port}"}], requests
            finally:
                server.shutdown()
                adb("reverse", "--remove", f"tcp:{port}")
                (OUT / "mitm-origin.json").write_text(json.dumps(requests, indent=2) + "\n")


def native_regressions():
    log = OUT / "native-tests.txt"
    try:
        # Write directly to disk so a timeout preserves partial instrumentation output.
        with mitm_origin() as arguments, log.open("w") as output:
            subprocess.run([
                "adb", "shell", "am", "instrument", "-w", "-r",
                *arguments,
                PACKAGE + ".test/androidx.test.runner.AndroidJUnitRunner",
            ], stdout=output, stderr=subprocess.STDOUT, timeout=360, check=True)
    finally:
        if log.exists():
            print(log.read_text())
    assert re.search(r"^OK \([1-9]\d* tests?\)\s*$", log.read_text(), re.MULTILINE), \
        "Native regressions failed or ran no tests; see native-tests.txt"


def diagnostics(name):
    try:
        capture(f"{name}-last-screen")
    except (subprocess.SubprocessError, OSError) as error:
        print(f"Screenshot capture failed: {error}")
    try:
        with (OUT / f"{name}-logcat.txt").open("wb") as log:
            # Some emulator logcat versions exit 255 after dumping their buffers.
            subprocess.run(["adb", "logcat", "-d", "-t", "5000"], stdout=log,
                           stderr=subprocess.STDOUT, timeout=45, check=False)
    except (subprocess.SubprocessError, OSError) as error:
        print(f"Log collection failed: {error}")


def main():
    OUT.mkdir(parents=True, exist_ok=True)
    results = {}
    try:
        apks = list(Path("build/apk").rglob("*.apk"))
        application, = [apk for apk in apks if not apk.name.endswith("-androidTest.apk")]
        instrumentation, = [apk for apk in apks if apk.name.endswith("-androidTest.apk")]
        for apk in (application, instrumentation):
            adb("install", "--no-streaming", "-r", str(apk), timeout=240)
        for name, check in (("ui", ui_smoke), ("native", native_regressions)):
            try:
                # Each suite owns a fresh installation state, even if the UI suite failed mid-start.
                adb("shell", "am", "force-stop", PACKAGE)
                assert "Success" in adb("shell", "pm", "clear", PACKAGE)
                adb("logcat", "-c")
                check()
                results[name] = {"passed": True}
            except Exception as error:
                results[name] = {"passed": False, "error": str(error)}
                (OUT / f"{name}-failure.txt").write_text(traceback.format_exc())
                traceback.print_exc()
            finally:
                diagnostics(name)
    except Exception as error:
        results["setup"] = {"passed": False, "error": str(error)}
        diagnostics("setup")
        traceback.print_exc()
    finally:
        (OUT / "results.json").write_text(json.dumps({
            "sha": os.environ.get("GITHUB_SHA"), "checks": results,
        }, indent=2) + "\n")
        try:
            adb("shell", "am", "force-stop", PACKAGE)
        except (subprocess.SubprocessError, OSError) as error:
            print(f"App cleanup failed: {error}")
    if set(results) != {"ui", "native"} or not all(result["passed"] for result in results.values()):
        raise SystemExit("Android checks failed; see build/android-smoke/results.json")
    print("Android UI smoke and native regressions passed")


if __name__ == "__main__":
    main()
