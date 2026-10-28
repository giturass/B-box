"""Exercise the real Flutter host, Room profiles and libbox on a CI emulator."""
import functools
import http.server
import json
import re
import subprocess
import threading
import time
import urllib.parse
import xml.etree.ElementTree as ET
from pathlib import Path

PACKAGE = "io.ericlee.bfa"
COMPONENT = PACKAGE + "/io.nekohasekai.sfa.compose.MainActivity"
OUT = Path("build/android-smoke")
OUT.mkdir(parents=True, exist_ok=True)


def adb(*args, binary=False, timeout=45):
    return subprocess.check_output(["adb", *args], text=not binary, timeout=timeout)


def nodes():
    for attempt in range(4):
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
    return [n for n in items if label in (n.get("text", "") + "\n" + n.get("content-desc", "")).splitlines()]


def wait_for(label, timeout=90):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        found = matching(label, nodes())
        if found:
            return found
        time.sleep(0.5)
    raise AssertionError(f"UI never showed {label!r}; see window.xml")


def tap_node(item):
    x1, y1, x2, y2 = map(int, re.findall(r"\d+", item.attrib["bounds"]))
    adb("shell", "input", "tap", str((x1 + x2) // 2), str((y1 + y2) // 2))
    time.sleep(0.6)


def click(label):
    found = wait_for(label)
    exact = [n for n in found if n.get("text") == label or n.get("content-desc") == label]
    item = (exact or found)[-1]
    if label in ("Start", "Stop"):
        assert exact, "The VPN button must have its own accessible tap target"
    tap_node(item)


def capture(name):
    (OUT / f"{name}.png").write_bytes(adb("exec-out", "screencap", "-p", binary=True))


config = {
    "log": {"level": "info"},
    "inbounds": [{"type": "tun", "address": ["172.19.0.1/30"], "auto_route": True}],
    "outbounds": [{"type": "selector", "tag": "Smoke selector", "outbounds": ["direct"]}, {"type": "direct", "tag": "direct"}],
    "route": {"final": "Smoke selector"},
    "experimental": {"clash_api": {"default_mode": "rule"}},
}
(OUT / "profile.json").write_text(json.dumps(config))
handler = functools.partial(http.server.SimpleHTTPRequestHandler, directory=str(OUT))
server = http.server.ThreadingHTTPServer(("127.0.0.1", 18763), handler)
threading.Thread(target=server.serve_forever, daemon=True).start()
try:
    apk, = Path("build/apk").glob("*.apk")
    adb("install", "--no-streaming", "-r", str(apk), timeout=240)
    adb("shell", "pm", "clear", PACKAGE)
    adb("logcat", "-c")
    adb("reverse", "tcp:18763", "tcp:18763")
    adb("shell", "am", "start", "-W", "-n", COMPONENT)
    wait_for("Dashboard")
    startup = nodes()
    for label in ("Disclaimer", "免责声明", "I agree", "Agree and continue"):
        assert not matching(label, startup), "Fresh installation must open the dashboard without a disclaimer gate"
    capture("01-startup")
    uri = "sing-box://import-remote-profile?" + urllib.parse.urlencode({"url": "http://127.0.0.1:18763/profile.json"}) + "#" + urllib.parse.quote("Smoke profile")
    # ADB's shell parses this argument again; quote the encoded URI.
    adb("shell", "am", "start", "-W", "-n", COMPONENT, "-a", "android.intent.action.VIEW", "-d", "'" + uri + "'")
    click("Save")
    wait_for("Smoke profile")
    capture("02-imported")
    click("Start")
    # Android may ask for notifications and VPN access, depending on its API.
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
    print("Real Android import, VPN permission, service, groups, stop and profile persistence passed")
finally:
    try:
        capture("last-screen")
    except (subprocess.SubprocessError, OSError) as error:
        print(f"Screenshot capture failed: {error}")
    # Some emulator logcat versions return 255 after dumping their buffers.
    # Preserve that output and the original test failure independently.
    with (OUT / "logcat.txt").open("wb") as log:
        try:
            subprocess.run(["adb", "logcat", "-d", "-t", "5000"], stdout=log, stderr=subprocess.STDOUT, timeout=45)
        except subprocess.SubprocessError as error:
            print(f"Log collection failed: {error}")
    server.shutdown()
