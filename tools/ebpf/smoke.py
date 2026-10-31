"""CI-only: exercise real OpenSSL uprobes and owner-EOF cleanup on Linux."""
import base64
import json
import os
from pathlib import Path
import selectors
import socket
import subprocess
import tempfile
import time

ROOT = Path(__file__).resolve().parent


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
    base = Path.home() / "tmp"
    base.mkdir(exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="bbox-ebpf-", dir=base) as directory:
        directory = Path(directory)
        cert, key = directory / "server.crt", directory / "server.key"
        # Ephemeral server identity for this test, never installed as a trusted CA.
        subprocess.run(["openssl", "req", "-x509", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:P-256",
                        "-nodes", "-days", "1", "-subj", "/CN=capture.test",
                        "-addext", "basicConstraints=critical,CA:FALSE", "-out", str(cert), "-keyout", str(key)],
                       check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        server = subprocess.Popen(["openssl", "s_server", "-cert", str(cert), "-key", str(key),
                                   "-accept", f"127.0.0.1:{port}", "-www", "-quiet"],
                                  stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
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
            library = next(path for path in ["/usr/lib/x86_64-linux-gnu/libssl.so.3", "/lib/x86_64-linux-gnu/libssl.so.3"]
                           if Path(path).is_file())
            collector = subprocess.Popen([str(ROOT / "capture")], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                         stderr=subprocess.PIPE, bufsize=0)
            config = {"excludeUid": 4294967295, "userId": os.getuid() // 100000,
                      "uids": [os.getuid()], "packages": {}, "libraries": [library]}
            collector.stdin.write((json.dumps(config) + "\n").encode())
            collector.stdin.flush()
            ready = event(collector.stdout)
            assert ready["type"] == "ready" and ready["libraries"] > 0, ready
            client = subprocess.Popen(["openssl", "s_client", "-connect", f"127.0.0.1:{port}",
                                       "-servername", "capture.test", "-quiet", "-ign_eof"],
                                      stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
            request = b"GET /ebpf-smoke HTTP/1.1\r\nHost: capture.test\r\nX-Test: " + b"x" * 6000 + b"\r\nConnection: close\r\n\r\n"
            response, _ = client.communicate(request, timeout=10)
            assert b"HTTP/1.0 200" in response, response[:200]
            output = {"write": bytearray(), "read": bytearray()}
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline:
                value = event(collector.stdout, 5)
                if value.get("pid") != client.pid:
                    continue
                if value["type"] == "data":
                    assert not value.get("gap"), value
                    output[value["direction"]].extend(base64.b64decode(value.get("data", "")))
                if value["type"] == "close" and output["read"]:
                    break
            assert output["write"] == request, "SSL_write/_ex data duplicated, reordered or lost"
            assert output["read"] == response, "SSL_read/_ex data duplicated, reordered or lost"
            collector.stdin.close()
            assert collector.wait(timeout=5) == 0, collector.stderr.read().decode()
            print("Real TLS plaintext matched both directions; owner EOF stopped the collector.")
        finally:
            if collector is not None and collector.poll() is None:
                collector.kill()
                collector.wait()
            server.terminate()
            server.wait(timeout=5)


if __name__ == "__main__":
    main()
