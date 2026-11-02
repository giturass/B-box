"""CI-only Go TLS 1.2/1.3, socket, pcapng and independent tshark decryption.

Use a fixture built by each supported Go version, including stripped PIE.
The latency and /proc samples describe this Linux fixture, not Android apps.
"""
import argparse
import base64
import json
import os
from pathlib import Path
import statistics
import struct
import subprocess
import tempfile
import threading
import time

from smoke import event, fixture_environment, stop_collector

ROOT = Path(__file__).resolve().parent


def block(kind, body):
    body += bytes(-len(body) % 4)
    length = len(body) + 12
    return struct.pack("<II", kind, length) + body + struct.pack("<I", length)


def write_pcapng(path, messages):
    # Independent standard-format fixture writer. The core writer is also
    # checked by wire_test.go against block sizes, timestamps, bytes and DSBs.
    data = block(0x0A0D0D0A, struct.pack("<IHHq", 0x1A2B3C4D, 1, 0, -1))
    data += block(1, struct.pack("<HHIHHB3xI", 101, 0, 32768, 9, 1, 9, 0))
    # Secrets must precede packets even if their perf events arrived later.
    for value in sorted(messages, key=lambda value: value["type"] != "secret"):
        if value["type"] == "secret":
            line = (value["label"] + " " + base64.b64decode(value["clientRandom"]).hex() + " "
                    + base64.b64decode(value["secret"]).hex() + "\n").encode()
            data += block(10, struct.pack("<II", 0x544C534B, len(line)) + line)
        elif value["type"] == "packet":
            packet, at = base64.b64decode(value["data"]), value["timestampNs"]
            data += block(6, struct.pack("<IIIII", 0, at >> 32, at & 0xFFFFFFFF, len(packet), value["packetLength"]) + packet)
    path.write_bytes(data)


def process_sample(pid):
    stat = Path(f"/proc/{pid}/stat").read_text().rsplit(")", 1)[1].split()
    # Fields after comm start at field 3; utime/stime are fields 14/15.
    ticks = int(stat[11]) + int(stat[12])
    rss = int(stat[21]) * os.sysconf("SC_PAGE_SIZE")
    return {"cpuSeconds": ticks / os.sysconf("SC_CLK_TCK"), "rssBytes": rss}


def capture_complete(events, count, version, port):
    opened = {value["id"] for value in events if value["type"] == "open"}
    closed = {value["id"] for value in events if value["type"] == "close"}
    if len(opened) != count or not opened <= closed:
        return False
    labels = {"CLIENT_RANDOM"} if version == 12 else {
        "CLIENT_HANDSHAKE_TRAFFIC_SECRET", "SERVER_HANDSHAKE_TRAFFIC_SECRET",
        "CLIENT_TRAFFIC_SECRET_0", "SERVER_TRAFFIC_SECRET_0",
    }
    for identity in opened:
        if not labels <= {value["label"] for value in events if value["type"] == "secret" and value["id"] == identity}:
            return False
    finished = set()
    for value in events:
        if value["type"] != "packet":
            continue
        packet = base64.b64decode(value["data"])
        # This fixture binds IPv4 loopback. A server FIN/RST follows its
        # response and proves that the independent AF_PACKET reader has
        # reached this connection's end, beyond the TLS uprobe close event.
        if len(packet) < 40 or packet[0] >> 4 != 4:
            continue
        tcp = (packet[0] & 15) * 4
        if tcp >= 20 and len(packet) >= tcp + 20 and struct.unpack_from(">H", packet, tcp)[0] == port and packet[tcp + 13] & 5:
            finished.add(value["id"])
    return opened <= finished


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("fixture", type=Path)
    parser.add_argument("--requests", type=int, default=3)
    args = parser.parse_args()
    fixture = args.fixture.resolve()
    assert fixture.is_file() and 1 <= args.requests <= 100
    output = ROOT / "build" / "gotls" / fixture.name
    output.mkdir(parents=True, exist_ok=True)
    base, uid = fixture_environment()
    identity = {"user": uid, "group": uid, "extra_groups": []}
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
                      "packages": {}, "libraries": [str(fixture)], "captureNetwork": True, "captureTlsKeys": True}
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
            assert any("network packet capture ready" in item.get("stage", "") for item in startup), "raw packet socket did not open"

            def drain():
                for line in collector.stdout:
                    try:
                        value = json.loads(line)
                        messages.append(value)
                        if value["type"] in ("error", "packet_loss"):
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
                    complete = capture_complete(client_events, args.requests, version, port)
                    if complete or failures:
                        break
                    time.sleep(.05)
                # Preserve partial wire evidence too, if any later assertion
                # fails. Never interpret TLS close as an AF_PACKET barrier.
                (output / f"tls-{version}.capture.json").write_text(json.dumps({
                    "port": port,
                    "uris": [f"/go-tls-{version}-{index}" for index in range(args.requests)],
                    "events": [value for value in client_events if value["type"] in ("packet", "secret")],
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
                assert any(value["type"] == "secret" for value in client_events), "no TLS keys with KeyLogWriter unset"
                assert any(value["type"] == "packet" for value in client_events), "no original network packets"
                assert complete, "TLS events, keys and network packets did not all finish within the capture deadline"
                pcap = output / f"tls-{version}.pcapng"
                write_pcapng(pcap, client_events)
                decoded = subprocess.check_output(["tshark", "-r", str(pcap), "-d", f"tcp.port=={port},tls",
                                                   "-Y", "http.request", "-T", "fields", "-e", "http.request.uri"], stderr=subprocess.PIPE, timeout=20).decode()
                for index in range(args.requests):
                    assert f"/go-tls-{version}-{index}" in decoded, "Wireshark could not independently decrypt captured packets and DSB keys"
                measurements[f"tls{version}RequestsDecrypted"] = args.requests
            after = process_sample(collector.pid)
            measurements.update({"collectorCpuSeconds": after["cpuSeconds"] - before["cpuSeconds"],
                                 "collectorRssBytes": after["rssBytes"], "reportedLosses": len(failures)})
            assert not failures, failures
            assert stop_collector(collector) == 0, collector.stderr.read().decode()
            reader.join(timeout=2)
            (output / "results.json").write_text(json.dumps(measurements, indent=2) + "\n")
            print(f"{fixture.name}: Go TLS plaintext, socket identity, TLS 1.2/1.3 keys and tshark decryption passed.")
        finally:
            (output / "diagnostics.json").write_text(json.dumps({
                "measurements": measurements, "failures": failures,
                "startup": locals().get("startup", []),
                "events": [value for value in messages if value["type"] not in ("data", "packet", "secret")],
            }, indent=2) + "\n")
            for process in clients + [collector, server]:
                if process is not None and process.poll() is None:
                    process.kill()
                    process.wait()


if __name__ == "__main__":
    main()
