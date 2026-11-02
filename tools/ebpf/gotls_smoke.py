"""CI-only Go TLS 1.2/1.3 plaintext and socket correlation validation.

Use a fixture built by each supported Go version, including stripped PIE.
The latency and /proc samples describe this Linux fixture, not Android apps.
"""
import argparse
import base64
import json
import os
from pathlib import Path
import statistics
import subprocess
import tempfile
import threading
import time

from smoke import event, fixture_environment, stop_collector

ROOT = Path(__file__).resolve().parent


def process_sample(pid):
    stat = Path(f"/proc/{pid}/stat").read_text().rsplit(")", 1)[1].split()
    # Fields after comm start at field 3; utime/stime are fields 14/15.
    ticks = int(stat[11]) + int(stat[12])
    rss = int(stat[21]) * os.sysconf("SC_PAGE_SIZE")
    return {"cpuSeconds": ticks / os.sysconf("SC_CLK_TCK"), "rssBytes": rss}


def capture_complete(events, count):
    opened = {value["id"] for value in events if value["type"] == "open"}
    closed = {value["id"] for value in events if value["type"] == "close"}
    sockets = {value["id"] for value in events if value["type"] == "socket" and "socket" in value}
    # Close is emitted after both plaintext directions. Socket metadata can
    # cross CPUs and arrive later, so wait for each endpoint as well.
    return len(opened) == count and opened <= closed and opened <= sockets


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("fixture", type=Path)
    parser.add_argument("--requests", type=int, default=3)
    args = parser.parse_args()
    fixture = args.fixture.resolve()
    assert fixture.is_file() and 1 <= args.requests <= 100
    output = ROOT / "build" / "gotls" / fixture.name
    output.mkdir(parents=True, exist_ok=True)
    base, uid, gid = fixture_environment()
    identity = {"user": uid, "group": gid, "extra_groups": []}
    with tempfile.TemporaryDirectory(prefix="bbox-gotls-", dir=base) as directory:
        directory = Path(directory)
        directory.chmod(0o755)
        cert, key = directory / "server.crt", directory / "server.key"
        subprocess.run(["openssl", "req", "-x509", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:P-256", "-nodes",
                        "-days", "1", "-subj", "/CN=capture.test", "-addext", "subjectAltName=DNS:capture.test",
                        "-out", str(cert), "-keyout", str(key)], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        os.chown(key, uid, uid)
        server = subprocess.Popen([str(fixture), "-serve", "-cert", str(cert), "-key", str(key)],
                                  stdout=subprocess.PIPE, stderr=subprocess.PIPE, **identity)
        collector = None
        messages, failures, clients, measurements = [], [], [], {}
        try:
            address = server.stdout.readline().decode().strip()
            assert address.startswith("127.0.0.1:"), "fixture did not listen on loopback"
            port = int(address.rsplit(":", 1)[1])
            for version in (12, 13):
                baseline = json.loads(subprocess.check_output([str(fixture), "-address", address, "-cert", str(cert), "-version", str(version), "-count", str(args.requests)], timeout=25, **identity))
                measurements[f"tls{version}BaselineMedianRequestMs"] = statistics.median(item["ElapsedNS"] for item in baseline) / 1e6
            measurements["requestsPerProtocol"] = args.requests
            collector = subprocess.Popen([str(ROOT / "capture")], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, bufsize=0)
            config = {"excludeUid": 4294967295, "userId": uid // 100000, "uids": [uid],
                      "packages": {}, "libraries": [str(fixture)]}
            collector.stdin.write((json.dumps(config) + "\n").encode())
            collector.stdin.flush()
            startup = []
            deadline = time.monotonic() + 25
            while True:
                value = event(collector.stdout, max(.1, deadline - time.monotonic()))
                startup.append(value)
                assert time.monotonic() < deadline, "collector startup timeout"
                if value["type"] == "ready":
                    break
            assert any("socket correlation attached" in item.get("stage", "") for item in startup), "socket CO-RE did not attach: " + str(startup)

            def drain():
                for line in collector.stdout:
                    try:
                        value = json.loads(line)
                        messages.append(value)
                        if value["type"] == "error":
                            failures.append(value.get("error", "capture loss"))
                    except Exception as error:
                        failures.append(str(error))

            reader = threading.Thread(target=drain, daemon=True)
            reader.start()
            before = process_sample(collector.pid)
            for version in (12, 13):
                client = subprocess.Popen([str(fixture), "-address", address, "-cert", str(cert), "-version", str(version), "-count", str(args.requests)],
                                          stdout=subprocess.PIPE, stderr=subprocess.PIPE, **identity)
                clients.append(client)
                stdout, stderr = client.communicate(timeout=25)
                assert client.returncode == 0, stderr.decode()
                transactions = json.loads(stdout)
                measurements[f"tls{version}CaptureMedianRequestMs"] = statistics.median(item["ElapsedNS"] for item in transactions) / 1e6
                deadline = time.monotonic() + 10
                complete = False
                while time.monotonic() < deadline:
                    client_events = [value for value in messages if value.get("id", "").startswith(f"{client.pid}-")]
                    complete = capture_complete(client_events, args.requests)
                    if complete or failures:
                        break
                    time.sleep(.05)
                # Retain the observed plaintext and lifecycle if validation fails.
                (output / f"tls-{version}.capture.json").write_text(json.dumps({
                    "port": port,
                    "uris": [f"/go-tls-{version}-{index}" for index in range(args.requests)],
                    "events": client_events,
                }) + "\n")
                opens = [value for value in client_events if value["type"] == "open"]
                assert len(opens) == args.requests and all(value.get("adapter") == "Go TLS" and value.get("uid") == uid for value in opens), "Go handshakes were not captured for the target UID"
                flows = {value["id"]: {"write": bytearray(), "read": bytearray()} for value in opens}
                for value in client_events:
                    if value["type"] == "data":
                        assert not value.get("gap"), value.get("error", "Go TLS stream gap")
                        flows[value["id"]][value["direction"]].extend(base64.b64decode(value.get("data", "")))
                expected = {base64.b64decode(item["Request"]): base64.b64decode(item["Response"]) for item in transactions}
                assert len(flows) == len(expected)
                for flow in flows.values():
                    request = bytes(flow["write"])
                    assert request in expected and bytes(flow["read"]) == expected[request], "Go plaintext duplicated, lost or paired with another goroutine"
                sockets = [value["socket"] for value in client_events if value["type"] == "socket" and "socket" in value]
                assert len(sockets) == args.requests and all(value["destination"] == address and value["fd"] >= 0 for value in sockets), "socket endpoint/generation mismatch"
                assert complete, "TLS plaintext and socket events did not finish within the capture deadline"
                measurements[f"tls{version}RequestsCaptured"] = args.requests
            after = process_sample(collector.pid)
            measurements.update({"collectorCpuSeconds": after["cpuSeconds"] - before["cpuSeconds"],
                                 "collectorRssBytes": after["rssBytes"], "reportedLosses": len(failures)})
            assert not failures, failures
            assert stop_collector(collector) == 0, collector.stderr.read().decode()
            reader.join(timeout=2)
            (output / "results.json").write_text(json.dumps(measurements, indent=2) + "\n")
            print(f"{fixture.name}: Go TLS 1.2/1.3 plaintext and socket identity passed.")
        finally:
            (output / "diagnostics.json").write_text(json.dumps({
                "measurements": measurements, "failures": failures,
                "startup": locals().get("startup", []),
                "events": [value for value in messages if value["type"] != "data"],
            }, indent=2) + "\n")
            for process in clients + [collector, server]:
                if process is not None and process.poll() is None:
                    process.kill()
                    process.wait()


if __name__ == "__main__":
    main()
