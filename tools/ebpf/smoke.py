"""CI-only: exercise real OpenSSL uprobes and owner-EOF cleanup on Linux."""
import base64
from contextlib import contextmanager
import json
import os
from pathlib import Path
import platform
import pwd
import selectors
import signal
import socket
import subprocess
import sys
import tempfile
import threading
import time

ROOT = Path(__file__).resolve().parent


def fixture_environment():
    """Give this CI workload a UID separate from the collector and runner."""
    assert os.geteuid() == 0, "run the Linux smoke through sudo"
    caller = int(os.environ["SUDO_UID"])
    assert caller != 0, "the CI runner must be an ordinary user"
    base = Path(pwd.getpwuid(caller).pw_dir) / "tmp"
    base.mkdir(exist_ok=True)
    used = set()
    for process in Path("/proc").iterdir():
        if process.name.isdecimal():
            try:
                used.add(process.stat().st_uid)
            except FileNotFoundError:
                pass
    uid = next(value for value in range(60000, 61000) if value not in used)
    return base, uid


@contextmanager
def fixture_credentials(uid):
    """Run the in-process APK fixture as its target UID, then restore cleanup."""
    uids, gids, groups = os.getresuid(), os.getresgid(), os.getgroups()
    try:
        os.setgroups([])
        os.setresgid(uid, uid, gids[2])
        os.setresuid(uid, uid, uids[2])
        yield
    finally:
        os.seteuid(uids[1])
        os.setresuid(*uids)
        os.setresgid(*gids)
        os.setgroups(groups)


def stop_collector(process):
    """Keep the owner-EOF deadline, preserving stacks if cleanup stalls in CI."""
    process.stdin.close()
    try:
        return process.wait(timeout=5)
    except subprocess.TimeoutExpired:
        print(f"Collector {process.pid} did not stop within 5 seconds of owner EOF", file=sys.stderr)
        for task in sorted(Path(f"/proc/{process.pid}/task").glob("*")):
            try:
                print(f"Thread {task.name}: {task.joinpath('wchan').read_text().strip()}\n"
                      f"{task.joinpath('stack').read_text()}", file=sys.stderr)
            except OSError as error:
                print(f"Thread {task.name}: {error}", file=sys.stderr)
        # Only the CI collector is signalled. Drain stderr while Go prints its
        # goroutine stacks, including when normal test output uses a pipe.
        diagnostic = []
        reader = None
        if process.stderr is not None:
            reader = threading.Thread(target=lambda: diagnostic.append(process.stderr.read()), daemon=True)
            reader.start()
        try:
            process.send_signal(signal.SIGQUIT)
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=5)
        finally:
            if reader is not None:
                reader.join(timeout=2)
                for value in diagnostic:
                    print(value.decode(errors="replace"), file=sys.stderr)
        raise


def event(stream, timeout=20):
    selector = selectors.DefaultSelector()
    try:
        selector.register(stream, selectors.EVENT_READ)
        if not selector.select(timeout):
            raise AssertionError("collector event timeout")
        line = stream.readline()
        if not line:
            raise AssertionError("collector exited")
        value = json.loads(line)
        assert value["type"] != "error", value.get("error")
        return value
    finally:
        selector.close()


def main():
    base, uid = fixture_environment()
    identity = {"user": uid, "group": uid, "extra_groups": []}
    with tempfile.TemporaryDirectory(prefix="bbox-ebpf-", dir=base) as directory:
        directory = Path(directory)
        directory.chmod(0o755)
        cert, key = directory / "server.crt", directory / "server.key"
        # Ephemeral server identity for this test, never installed as a trusted CA.
        subprocess.run(["openssl", "req", "-x509", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:P-256",
                        "-nodes", "-days", "1", "-subj", "/CN=capture.test",
                        "-addext", "basicConstraints=critical,CA:FALSE", "-out", str(cert), "-keyout", str(key)],
                       check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        os.chown(key, uid, uid)
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        server = subprocess.Popen(["openssl", "s_server", "-cert", str(cert), "-key", str(key),
                                   "-accept", f"127.0.0.1:{port}", "-www", "-quiet"],
                                  stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, **identity)
        collector = None
        try:
            deadline = time.monotonic() + 5
            while True:
                try:
                    with socket.create_connection(("127.0.0.1", port), timeout=.2):
                        break
                except OSError:
                    assert time.monotonic() < deadline, "TLS fixture did not start"
                    time.sleep(.05)
            triplet = "aarch64-linux-gnu" if platform.machine() == "aarch64" else "x86_64-linux-gnu"
            library = next(path for path in [f"/usr/lib/{triplet}/libssl.so.3", f"/lib/{triplet}/libssl.so.3"]
                           if Path(path).is_file())
            collector = subprocess.Popen([str(ROOT / "capture")], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                         stderr=subprocess.PIPE, bufsize=0)
            config = {"excludeUid": 4294967295, "userId": uid // 100000,
                      "uids": [uid], "packages": {}, "libraries": [library]}
            collector.stdin.write((json.dumps(config) + "\n").encode())
            collector.stdin.flush()
            deadline = time.monotonic() + 25
            stages = []
            while True:
                remaining = deadline - time.monotonic()
                assert remaining > 0, f"collector startup timed out: {stages[-1:] or ['no progress']}"
                ready = event(collector.stdout, remaining)
                if ready["type"] != "progress":
                    break
                assert ready["version"] == 1 and ready.get("stage"), ready
                stages.append(ready["stage"])
            assert ready["type"] == "ready" and ready["libraries"] > 0, ready
            assert stages, "collector did not report startup stages"
            client = subprocess.Popen(["openssl", "s_client", "-connect", f"127.0.0.1:{port}",
                                       "-servername", "capture.test", "-quiet", "-ign_eof"],
                                      stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, **identity)
            request = b"GET /ebpf-smoke HTTP/1.1\r\nHost: capture.test\r\nX-Test: " + b"x" * 6000 + b"\r\nConnection: close\r\n\r\n"
            response, _ = client.communicate(request, timeout=10)
            assert b"HTTP/1.0 200" in response, response[:200]
            output = {"write": bytearray(), "read": bytearray()}
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline:
                value = event(collector.stdout, 5)
                if value.get("pid") != client.pid:
                    continue
                assert value.get("uid") == uid, "TLS data attributed to a different UID"
                if value["type"] == "data":
                    assert not value.get("gap"), {"event": value, "capturedBytes": {key: len(data) for key, data in output.items()}}
                    output[value["direction"]].extend(base64.b64decode(value.get("data", "")))
                if value["type"] == "close" and output["read"]:
                    break
            assert output["write"] == request, "SSL_write/_ex data duplicated, reordered or lost"
            assert output["read"] == response, "SSL_read/_ex data duplicated, reordered or lost"
            assert stop_collector(collector) == 0, collector.stderr.read().decode()
            print("Real TLS plaintext matched both directions; owner EOF stopped the collector.")
        finally:
            if collector is not None and collector.poll() is None:
                collector.kill()
                collector.wait()
            server.terminate()
            server.wait(timeout=5)


if __name__ == "__main__":
    main()
