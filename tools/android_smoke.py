"""Run UI and native regressions independently on a CI emulator."""

import functools
import base64
import contextlib
import http.server
import json
import os
import re
import select
import socket
import socketserver
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
TEST_RUNNER = PACKAGE + ".test/androidx.test.runner.AndroidJUnitRunner"
GESTURE_COMPONENT = PACKAGE + ".test/io.nekohasekai.sfa.flutter.SmokeGestureInstrumentation"
OUT = Path("build/android-smoke")
LOG_BUFFERS = ("-b", "main", "-b", "system", "-b", "crash", "-b", "events")


def adb(*args, binary=False, timeout=45):
    return subprocess.check_output(["adb", *args], text=not binary, timeout=timeout)


def launch_activity(*args):
    result = adb("shell", "am", "start", "-W", "-n", COMPONENT, *args)
    with (OUT / "activity-starts.txt").open("a") as log:
        log.write(f"Arguments: {args!r}\n{result}\n")
    print(result, flush=True)


@contextlib.contextmanager
def record_logcat(name):
    # Stream before launching the app: a tail collected after a UI timeout can
    # discard the startup failure amid the emulator's background activity.
    with (OUT / f"{name}-logcat.txt").open("wb") as log:
        process = subprocess.Popen(
            ["adb", "logcat", *LOG_BUFFERS, "-v", "threadtime"],
            stdout=log, stderr=subprocess.STDOUT,
        )
        try:
            yield
        finally:
            process.terminate()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)


def device_page_size():
    try:
        return int(adb("shell", "getconf", "PAGE_SIZE").strip()), "getconf"
    except (subprocess.CalledProcessError, ValueError):
        # API 24's shell has no getconf. Read the current shell command's maps
        # rather than guessing a page size from the Android version or ABI.
        maps = adb("shell", "cat", "/proc/self/smaps")
        sizes = [int(value) * 1024 for value in re.findall(
            r"^KernelPageSize:\s+(\d+)\s+kB\s*$", maps, re.MULTILINE)]
        if not sizes:
            raise RuntimeError("Could not read the device's page size")
        return min(sizes), "/proc/self/smaps"


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


def wait_for(label, timeout=90, dismiss_launcher_anr=False, whole_window=False, stable=False):
    deadline = time.monotonic() + timeout
    previous_bounds = None
    while time.monotonic() < deadline:
        current = nodes()
        found = matching(label, current)
        if found:
            bounds = tuple(node.get("bounds") for node in found)
            if not stable or bounds == previous_bounds:
                return current if whole_window else found
            previous_bounds = bounds
        else:
            previous_bounds = None
        # Google APIs images can start with a Pixel Launcher ANR over our app.
        # Dismiss only that known emulator failure, once; B-box ANRs still fail.
        if dismiss_launcher_anr and matching("Pixel Launcher isn't responding", current):
            close = [node for node in current if node.get("resource-id") == "android:id/aerr_close"]
            if close:
                dismiss_launcher_anr = False
                capture("launcher-anr")
                tap_node(close[0])
                launch_activity()
        time.sleep(0.5)
    raise AssertionError(f"UI never showed {label!r}; see window.xml")


def tap_node(item):
    x1, y1, x2, y2 = map(int, re.findall(r"\d+", item.attrib["bounds"]))
    adb("shell", "input", "tap", str((x1 + x2) // 2), str((y1 + y2) // 2))
    time.sleep(0.6)


def check_instrumentation_targets():
    listing = adb("shell", "pm", "list", "instrumentation")
    with (OUT / "instrumentation-targets.txt").open("a") as log:
        log.write(listing + "\n")
    registered = re.findall(r"^instrumentation:(\S+) \(target=([^\)]+)\)\s*$", listing, re.MULTILINE)
    for component, target in ((TEST_RUNNER, PACKAGE), (GESTURE_COMPONENT, PACKAGE + ".test")):
        targets = [package for name, package in registered if name == component]
        assert targets == [target], f"Unexpected instrumentation target for {component}: {targets!r}"


def hold_node(item):
    bounds = item.attrib["bounds"]
    match = re.fullmatch(r"\[(\d+),(\d+)\]\[(\d+),(\d+)\]", bounds)
    assert match, f"Invalid long-press bounds: {bounds!r}"
    x1, y1, x2, y2 = map(int, match.groups())
    assert x2 > x1 and y2 > y1, f"Empty long-press bounds: {bounds!r}"
    x, y = x1 + (x2 - x1) // 5, (y1 + y2) // 2
    duration = 1500
    # Android 7's shell swipe uses TOOL_TYPE_UNKNOWN, which Flutter correctly
    # filters out of touch-only long-press recognizers. Inject a real finger
    # through a self-targeting test runner without restarting the current app.
    check_instrumentation_targets()
    original_pid = adb("shell", "pidof", PACKAGE).split()
    assert original_pid, "B-box must already be running for a long press"
    request = f"Finger long press at ({x}, {y}), {duration} ms, bounds={bounds}, pid={original_pid}"
    print(request, flush=True)
    with (OUT / "gesture-injections.txt").open("a") as log:
        log.write(request + "\n")
        result = adb("shell", "am", "instrument", "-w", "-r",
                     "-e", "x", str(x), "-e", "y", str(y), "-e", "durationMs", str(duration),
                     GESTURE_COMPONENT)
        log.write(result + "\n")
    print(result, flush=True)
    assert re.search(r"^INSTRUMENTATION_RESULT: bbox_gesture_ok=true\s*$", result, re.MULTILINE), \
        "Finger injection failed; see gesture-injections.txt"
    assert re.search(r"^INSTRUMENTATION_CODE: -1\s*$", result, re.MULTILINE), \
        "Gesture instrumentation did not finish successfully"
    assert adb("shell", "pidof", PACKAGE).split() == original_pid, \
        "Gesture instrumentation restarted B-box"
    time.sleep(0.6)


def click(label, window=None, stable=False):
    found = wait_for(label, stable=stable) if window is None else matching(label, window)
    assert found, f"No {label!r} action in the observed window"
    exact = [node for node in found if node.get("text") == label or node.get("content-desc") == label]
    if label in ("Start", "Stop"):
        assert exact, "The VPN button must have its own accessible tap target"
    target = (exact or found)[-1]
    print(f"Tap {label!r} at {target.get('bounds')}", flush=True)
    tap_node(target)


def capture(name):
    (OUT / f"{name}.png").write_bytes(adb("exec-out", "screencap", "-p", binary=True))


@contextlib.contextmanager
def subscription_proxy():
    """Expose a CI-only SOCKS5 TCP proxy through the emulator's loopback.

    The imported default configuration downloads its actual remote rule sets
    through this proxy. The runner must have DNS and TCP access to those URLs.
    """
    stopping = threading.Event()
    connection_lock = threading.Lock()
    connections = set()
    requests = []

    class SocksProxy(socketserver.BaseRequestHandler):
        def read_exact(self, size):
            value = b""
            while len(value) < size:
                chunk = self.request.recv(size - len(value))
                if not chunk:
                    raise ConnectionError("SOCKS client closed during its request")
                value += chunk
            return value

        def handle(self):
            self.request.settimeout(15)
            with connection_lock:
                if stopping.is_set():
                    return
                connections.add(self.request)
            upstream = None
            attempt = None
            try:
                version, method_count = self.read_exact(2)
                methods = self.read_exact(method_count)
                if version != 5 or 0 not in methods:
                    self.request.sendall(b"\x05\xff")
                    return
                self.request.sendall(b"\x05\x00")
                version, command, reserved, address_type = self.read_exact(4)
                if version != 5 or reserved != 0 or command != 1:
                    self.request.sendall(b"\x05\x07\x00\x01" + b"\x00" * 6)
                    return
                if address_type == 1:
                    host = socket.inet_ntop(socket.AF_INET, self.read_exact(4))
                elif address_type == 4:
                    host = socket.inet_ntop(socket.AF_INET6, self.read_exact(16))
                elif address_type == 3:
                    host = self.read_exact(self.read_exact(1)[0]).decode("ascii")
                else:
                    self.request.sendall(b"\x05\x08\x00\x01" + b"\x00" * 6)
                    return
                port = int.from_bytes(self.read_exact(2), "big")
                attempt = {"host": host, "port": port, "connected": False}
                requests.append(attempt)
                upstream = socket.create_connection((host, port), timeout=15)
                with connection_lock:
                    if stopping.is_set():
                        return
                    connections.add(upstream)
                attempt["connected"] = True
                self.request.sendall(b"\x05\x00\x00\x01" + b"\x00" * 6)
                readers = [self.request, upstream]
                while readers and not stopping.is_set():
                    readable, _, _ = select.select(readers, [], [], 1)
                    for source in readable:
                        destination = upstream if source is self.request else self.request
                        chunk = source.recv(64 * 1024)
                        if chunk:
                            destination.sendall(chunk)
                        else:
                            readers.remove(source)
                            destination.shutdown(socket.SHUT_WR)
            except (OSError, ValueError, UnicodeError) as error:
                if attempt is not None and not stopping.is_set():
                    attempt["error"] = str(error)
                if upstream is None:
                    with contextlib.suppress(OSError):
                        self.request.sendall(b"\x05\x01\x00\x01" + b"\x00" * 6)
            finally:
                with connection_lock:
                    connections.discard(self.request)
                    connections.discard(upstream)
                if upstream is not None:
                    upstream.close()

    with socketserver.ThreadingTCPServer(("127.0.0.1", 0), SocksProxy) as server:
        port = server.server_address[1]
        worker = threading.Thread(target=server.serve_forever, name="subscription-proxy")
        worker.start()
        try:
            adb("reverse", f"tcp:{port}", f"tcp:{port}")
            yield port, requests
        finally:
            stopping.set()
            server.shutdown()
            with connection_lock:
                active = tuple(connections)
            for connection in active:
                with contextlib.suppress(OSError):
                    connection.shutdown(socket.SHUT_RDWR)
                connection.close()
            # ThreadingTCPServer joins the non-daemon request workers after
            # the open sockets are closed, including incomplete handshakes.
            server.server_close()
            worker.join(timeout=5)
            try:
                adb("reverse", "--remove", f"tcp:{port}")
            except (subprocess.SubprocessError, OSError) as error:
                print(f"Reverse-port cleanup failed: {error}")
            (OUT / "subscription-proxy.json").write_text(json.dumps(requests, indent=2) + "\n")


def ui_smoke():
    class SubscriptionHandler(http.server.SimpleHTTPRequestHandler):
        timeout = 15

    class SubscriptionHTTPServer(http.server.ThreadingHTTPServer):
        daemon_threads = False

    handler = functools.partial(SubscriptionHandler, directory=str(OUT))
    with (subscription_proxy() as (proxy_port, proxy_requests),
          SubscriptionHTTPServer(("127.0.0.1", 0), handler) as server):
        # The actual URL bridge must import the supported node from a Base64
        # mixed subscription and retain an explicit report for skipped KCP.
        subscription = (
            f"socks5://127.0.0.1:{proxy_port}#Smoke%20node\n"
            "vless://11111111-1111-4111-8111-111111111111@127.0.0.1:443"
            "?security=tls&sni=example.test&type=kcp#Ignored%20KCP%20node\n"
        )
        (OUT / "profile.txt").write_text(base64.b64encode(subscription.encode()).decode() + "\n")
        port = server.server_port
        worker = threading.Thread(target=server.serve_forever, name="subscription-http")
        worker.start()
        try:
            adb("reverse", f"tcp:{port}", f"tcp:{port}")
            launch_activity()
            wait_for("Dashboard", dismiss_launcher_anr=True)
            capture("01-startup")
            uri = "sing-box://import-remote-profile?" + urllib.parse.urlencode({
                "url": f"http://127.0.0.1:{port}/profile.txt",
            }) + "#" + urllib.parse.quote("Smoke subscription")
            # ADB's shell parses the encoded URI a second time.
            launch_activity("-a", "android.intent.action.VIEW", "-d", "'" + uri + "'")
            # Prism's form sheet can expose semantics before it stops moving.
            # Confirm the button's position, then require the form to close;
            # its prefilled name alone does not establish a successful import.
            click("Save", stable=True)
            current = wait_for("Start", whole_window=True)
            assert matching("Profile file", current), "Import changed the default selection"
            click("Profiles", current)
            current = wait_for("Profile file", whole_window=True, stable=True)
            # File cards open their details from the long-press menu. The
            # dashboard panel also contains timestamp tap targets, so tapping
            # the center of its combined semantics need not open the details.
            cards = [node for node in matching("Profile file", current) if node.get("clickable") == "true"]
            assert len(cards) == 1, "The selected file card must be uniquely identifiable"
            hold_node(cards[0])
            click("Edit")
            click("Node subscriptions")
            current = wait_for("Smoke subscription", whole_window=True)
            source_text = "\n".join(node.get("text", "") + "\n" + node.get("content-desc", "") for node in current)
            assert re.search(r"\bNodes: 1\b", source_text), "The source must own exactly one supported node"
            assert re.search(r"\bSkipped: 1\b", source_text), "The source must report one skipped node"
            assert "the transport of this node is not supported by this core" in source_text, \
                "The source must show the unsupported KCP reason"
            capture("02-subscription-source")
            adb("shell", "input", "keyevent", "4")
            adb("shell", "input", "keyevent", "4")
            current = wait_for("Profile file", whole_window=True)
            assert matching("Profile file", current), "Default profile disappeared after import"
            assert not matching("Smoke subscription", current), "Subscription created another configuration file"
            assert not matching("Smoke profile", current), "Subscription created another configuration file"
            capture("02-profile-files")
            click("Dashboard")
            current = wait_for("Start", whole_window=True)
            assert matching("Profile file", current), "Node import changed the selected configuration"
            capture("02-imported")
            click("Start", current)
            for _ in range(12):
                current = nodes()
                if matching("Running", current):
                    break
                for label in ("Allow", "OK"):
                    if matching(label, current):
                        tap_node(matching(label, current)[-1])
                        break
                time.sleep(0.5)
            else:
                current = wait_for("Running", whole_window=True)
            capture("03-vpn-running")
            # Reuse the window that confirmed Running. A second UIAutomator
            # idle wait can time out on the continuously updating dashboard.
            click("Profiles", current)
            wait_for("proxy")
            wait_for("auto")
            current = wait_for("Smoke node", whole_window=True)
            assert not matching("Smoke selector", current), "Subscription group replaced the local core groups"
            assert matching("Profile file", current), "Selected profile is missing above the core proxy groups"
            # The selector's node precedes the automatic group's repeated
            # node in list layouts. Only the selector permits manual selection.
            choices = matching("Smoke node", current)
            clickable = [node for node in choices if node.get("clickable") == "true"]
            tap_node((clickable or choices)[0])
            connections_before_probe = len(proxy_requests)
            click("Test latency")
            deadline = time.monotonic() + 30
            while not any(request["connected"] for request in proxy_requests) and time.monotonic() < deadline:
                time.sleep(0.2)
            assert any(request["connected"] for request in proxy_requests), \
                "The imported SOCKS node never forwarded a TCP connection"
            # URL-test caching is owned by the core. Record new connections
            # without attributing an earlier rule-set download to this action.
            print(f"SOCKS connections before latency: {connections_before_probe}; "
                  f"after: {len(proxy_requests)}", flush=True)
            capture("04-core-proxies")
            click("Settings")
            wait_for("Appearance")
            capture("05-settings")
            click("Dashboard")
            click("Stop")
            wait_for("Not running")
            adb("shell", "am", "force-stop", PACKAGE)
            launch_activity()
            wait_for("Profile file")
            capture("05-restored-profile")
        finally:
            server.shutdown()
            server.server_close()
            worker.join(timeout=5)
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

        class TCPOrigin(socketserver.BaseRequestHandler):
            def handle(self):
                self.request.settimeout(10)
                self.request.sendall(b"\x00BBOX\xff")
                expected = b"\x00client\xff"
                payload = b""
                while len(payload) < len(expected):
                    part = self.request.recv(len(expected) - len(payload))
                    if not part:
                        break
                    payload += part
                assert payload == expected, payload
                requests.append({"protocol": "TLS/TCP", "payload": base64.b64encode(payload).decode("ascii")})
                self.request.sendall(b"\x00through-original-route\xff")
                # Exchange close_notify so both directions prove completion.
                self.request.unwrap().close()

        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.minimum_version = ssl.TLSVersion.TLSv1_2
        context.set_alpn_protocols(["http/1.1"])
        context.load_cert_chain(root / "origin.pem", root / "origin.key")
        with (http.server.ThreadingHTTPServer(("127.0.0.1", 0), Origin) as server,
              socketserver.ThreadingTCPServer(("127.0.0.1", 0), TCPOrigin) as tcp_server):
            port = server.server_port
            tcp_port = tcp_server.server_address[1]
            server.socket = context.wrap_socket(server.socket, server_side=True)
            tcp_server.socket = context.wrap_socket(tcp_server.socket, server_side=True)
            threading.Thread(target=server.serve_forever, daemon=True).start()
            threading.Thread(target=tcp_server.serve_forever, daemon=True).start()
            try:
                adb("reverse", f"tcp:{port}", f"tcp:{port}")
                adb("reverse", f"tcp:{tcp_port}", f"tcp:{tcp_port}")
                yield ["-e", "mitmOriginPort", str(port), "-e", "mitmOriginCA",
                       base64.b64encode((root / "root.pem").read_bytes()).decode("ascii"),
                       "-e", "mitmTCPOriginPort", str(tcp_port)]
                expected = [{"path": path, "host": f"origin.test:{port}"}
                            for path in ("/probe?raw=1;2", "/http-no-alpn")]
                expected.append({"protocol": "TLS/TCP", "payload": base64.b64encode(b"\x00client\xff").decode("ascii")})
                assert requests == expected, requests
            finally:
                server.shutdown()
                tcp_server.shutdown()
                for cleanup_port in (port, tcp_port):
                    try:
                        adb("reverse", "--remove", f"tcp:{cleanup_port}")
                    except (subprocess.SubprocessError, OSError) as error:
                        print(f"Reverse-port cleanup failed: {error}")
                (OUT / "mitm-origin.json").write_text(json.dumps(requests, indent=2) + "\n")


def native_regressions():
    log = OUT / "native-tests.txt"
    try:
        # Write directly to disk so a timeout preserves partial instrumentation output.
        with mitm_origin() as arguments, log.open("w") as output:
            subprocess.run([
                "adb", "shell", "am", "instrument", "-w", "-r",
                *arguments,
                TEST_RUNNER,
            ], stdout=output, stderr=subprocess.STDOUT, timeout=600, check=True)
    finally:
        if log.exists():
            print(log.read_text())
    assert re.search(r"^OK \([1-9]\d* tests?\)\s*$", log.read_text(), re.MULTILINE), \
        "Native regressions failed or ran no tests; see native-tests.txt"


def diagnostics(name, sdk=0):
    try:
        capture(f"{name}-last-screen")
    except (subprocess.SubprocessError, OSError) as error:
        print(f"Screenshot capture failed: {error}")
    log_path = OUT / f"{name}-logcat.txt"
    if not log_path.exists() or not log_path.stat().st_size:
        try:
            with log_path.open("wb") as log:
                # Also preserve setup failures that precede the log stream.
                subprocess.run(["adb", "logcat", *LOG_BUFFERS, "-d", "-v", "threadtime"],
                               stdout=log, stderr=subprocess.STDOUT, timeout=45, check=False)
        except (subprocess.SubprocessError, OSError) as error:
            print(f"Log collection failed: {error}")
    dumps = {"activities": ("activity", "activities"), "memory": ("meminfo",)}
    if sdk >= 30:
        dumps["exit-info"] = ("activity", "exit-info", PACKAGE)
    for suffix, arguments in dumps.items():
        try:
            with (OUT / f"{name}-{suffix}.txt").open("wb") as output:
                subprocess.run(["adb", "shell", "dumpsys", *arguments], stdout=output,
                               stderr=subprocess.STDOUT, timeout=45, check=False)
        except (subprocess.SubprocessError, OSError) as error:
            print(f"{suffix} collection failed: {error}")


def main():
    OUT.mkdir(parents=True, exist_ok=True)
    results = {}
    device = {}
    try:
        for name, prop in {
            "sdk": "ro.build.version.sdk",
            "release": "ro.build.version.release",
            "codename": "ro.build.version.codename",
            "fingerprint": "ro.build.fingerprint",
        }.items():
            device[name] = adb("shell", "getprop", prop).strip()
        device["page_size"], device["page_size_source"] = device_page_size()
        device["system_image"] = os.environ.get("BBOX_SMOKE_SYSTEM_IMAGE")
        expected_page_size = os.environ.get("BBOX_SMOKE_PAGE_SIZE")
        if expected_page_size:
            assert device["page_size"] == int(expected_page_size), \
                f"Expected {expected_page_size}-byte pages, got {device['page_size']}"
        apks = list(Path("build/apk").rglob("*.apk"))
        application, = [apk for apk in apks if not apk.name.endswith("-androidTest.apk")]
        instrumentation, = [apk for apk in apks if apk.name.endswith("-androidTest.apk")]
        for apk in (application, instrumentation):
            adb("install", "--no-streaming", "-r", str(apk), timeout=240)
        check_instrumentation_targets()
        for name, check in (("ui", ui_smoke), ("native", native_regressions)):
            try:
                # Each suite owns a fresh installation state, even if the UI suite failed mid-start.
                adb("shell", "am", "force-stop", PACKAGE)
                assert "Success" in adb("shell", "pm", "clear", PACKAGE)
                adb("logcat", *LOG_BUFFERS, "-c")
                with record_logcat(name):
                    check()
                results[name] = {"passed": True}
            except Exception as error:
                results[name] = {"passed": False, "error": str(error)}
                (OUT / f"{name}-failure.txt").write_text(traceback.format_exc())
                traceback.print_exc()
            finally:
                diagnostics(name, int(device.get("sdk", 0)))
    except Exception as error:
        results["setup"] = {"passed": False, "error": str(error)}
        diagnostics("setup", int(device.get("sdk", 0)))
        traceback.print_exc()
    finally:
        (OUT / "results.json").write_text(json.dumps({
            "sha": os.environ.get("GITHUB_SHA"), "device": device, "checks": results,
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
