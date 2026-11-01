"""CI-only Linux smoke: execute ELF text mapped directly from a stored APK entry.

The collector gets an APK path, never our independently resolved symbols. This
tests its automatic offsets and the actual inode instrumented by the kernel.
"""
import base64
import ctypes as C
import json
import os
from pathlib import Path
import queue
import struct
import subprocess
import sys
import tempfile
import threading
import time
import zipfile

ROOT = Path(__file__).resolve().parent
ALIGN = 16384


def stored_apk(path, data):
    entry = zipfile.ZipInfo("lib/x86_64/libcapture-fixture.so")
    padding = (-30 - len(entry.filename.encode()) - 4) % ALIGN
    entry.extra = struct.pack("<HH", 0xB0B0, padding) + bytes(padding)
    with zipfile.ZipFile(path, "w", compression=zipfile.ZIP_STORED) as archive:
        archive.writestr(entry, data)


class MappedLibrary:
    """Minimal loader for our relocation-free, BSS-free Linux x86_64 fixture."""

    def __init__(self, path):
        with zipfile.ZipFile(path) as archive:
            entry = archive.infolist()[0]
            assert entry.compress_type == zipfile.ZIP_STORED
            data = archive.read(entry)
        with open(path, "rb") as source:
            source.seek(entry.header_offset + 26)
            name_size, extra_size = struct.unpack("<HH", source.read(4))
            archive_offset = entry.header_offset + 30 + name_size + extra_size
            assert archive_offset % ALIGN == 0
            assert data[:7] == b"\x7fELF\x02\x01\x01"
            assert struct.unpack_from("<H", data, 18)[0] == 62  # EM_X86_64
            phoff, shoff = struct.unpack_from("<QQ", data, 32)
            phsize, phnum, shsize, shnum = struct.unpack_from("<HHHH", data, 54)
            segments = [struct.unpack_from("<IIQQQQQQ", data, phoff + i * phsize) for i in range(phnum)]
            segments = [segment for segment in segments if segment[0] == 1 and segment[6]]
            sections = [struct.unpack_from("<IIQQQQIIQQ", data, shoff + i * shsize) for i in range(shnum)]
            assert not any(s[1] in (4, 9, 19) and s[5] for s in sections), "fixture requires relocations"
            self.symbols = {}
            for section in sections:
                if section[1] != 11:  # SHT_DYNSYM
                    continue
                strings = sections[section[6]]
                names = data[strings[4]:strings[4] + strings[5]]
                for offset in range(section[4], section[4] + section[5], section[9]):
                    name, info, _, index, address, _ = struct.unpack_from("<IBBHQQ", data, offset)
                    if index and info & 0xF == 2:  # defined STT_FUNC
                        self.symbols[names[name:names.index(b"\0", name)].decode()] = address
            page = os.sysconf("SC_PAGESIZE")
            floor = lambda value: value // page * page
            ceil = lambda value: (value + page - 1) // page * page
            low = min(floor(s[3]) for s in segments)
            high = max(ceil(s[3] + s[6]) for s in segments)
            self.libc = C.CDLL(None, use_errno=True)
            self.libc.mmap.restype = C.c_void_p
            self.libc.mmap.argtypes = [C.c_void_p, C.c_size_t, C.c_int, C.c_int, C.c_int, C.c_longlong]
            self.libc.munmap.argtypes = [C.c_void_p, C.c_size_t]
            self.address = self._map(None, high - low, 0, 0x22, -1, 0)  # PRIVATE | ANONYMOUS
            self.size, self.bias = high - low, self.address - low
            try:
                for _, flags, offset, vaddr, _, filesz, memsz, _ in segments:
                    assert filesz == memsz, "fixture must not require a BSS loader"
                    assert vaddr % page == offset % page
                    protection = (1 if flags & 4 else 0) | (2 if flags & 2 else 0) | (4 if flags & 1 else 0)
                    target = self.bias + floor(vaddr)
                    mapped = self._map(target, ceil(vaddr % page + filesz), protection, 0x12,
                                       source.fileno(), archive_offset + floor(offset))  # PRIVATE | FIXED
                    assert mapped == target
            except BaseException:
                self.close()
                raise

    def _map(self, address, length, protection, flags, fd, offset):
        value = self.libc.mmap(address, length, protection, flags, fd, offset)
        if value == C.c_void_p(-1).value:
            raise OSError(C.get_errno(), "mmap APK fixture")
        return value

    def function(self, name, result, *arguments):
        return C.CFUNCTYPE(result, *arguments)(self.bias + self.symbols[name])

    def close(self):
        if self.address is not None:
            assert self.libc.munmap(self.address, self.size) == 0
            self.address = None


class SSLState(C.Structure):
    _fields_ = [("response", C.c_void_p), ("response_size", C.c_size_t), ("position", C.c_size_t),
                ("clear_result", C.c_int), ("block", C.c_int), ("entered", C.c_int), ("abandon", C.c_int),
                ("jump", C.c_void_p * 5)]


class SSLFixture:
    def __init__(self, library):
        self.library = library
        self.new = library.function("SSL_new", C.c_void_p, C.c_void_p)
        self.free = library.function("SSL_free", None, C.c_void_p)
        self.clear = library.function("SSL_clear", C.c_int, C.c_void_p)
        self.abandon = library.function("fixture_abandoned_write", C.c_int, C.c_void_p, C.c_void_p, C.c_int)
        self.states = []

    def session(self, response=b"fixture response"):
        buffer = C.create_string_buffer(response)
        state = SSLState(C.cast(buffer, C.c_void_p), len(response), 0, 1)
        self.states.append((state, buffer))  # keep user buffers and SSL pointers live
        assert self.new(C.byref(state)) == C.addressof(state)
        return state

    def write(self, state, data, symbol="SSL_write"):
        buffer = C.create_string_buffer(data)
        if symbol == "SSL_write":
            fn = self.library.function(symbol, C.c_int, C.c_void_p, C.c_void_p, C.c_int)
            assert fn(C.byref(state), buffer, len(data)) == len(data)
        else:
            fn = self.library.function(symbol, C.c_int, C.c_void_p, C.c_void_p, C.c_size_t, C.POINTER(C.c_size_t))
            count = C.c_size_t()
            assert fn(C.byref(state), buffer, len(data), C.byref(count)) == 1
            assert count.value == len(data)

    def read(self, state, length, symbol="SSL_read"):
        buffer = C.create_string_buffer(length)
        if symbol == "SSL_read":
            fn = self.library.function(symbol, C.c_int, C.c_void_p, C.c_void_p, C.c_int)
            count = fn(C.byref(state), buffer, length)
        else:
            fn = self.library.function(symbol, C.c_int, C.c_void_p, C.c_void_p, C.c_size_t, C.POINTER(C.c_size_t))
            size = C.c_size_t()
            expected = 2 if symbol == "SSL_read_early_data" else 1
            assert fn(C.byref(state), buffer, length, C.byref(size)) == expected
            count = size.value
        return buffer.raw[:count]


class Collector:
    def __init__(self, apk, directory):
        self.errors = open(directory / "collector.stderr", "w+b")
        self.process = subprocess.Popen([str(ROOT / "capture")], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                        stderr=self.errors, bufsize=0)
        self.queue = queue.Queue()
        self.flows, self.consumed, self.pids = {}, set(), set()
        self.reader = threading.Thread(target=self._read, daemon=True)
        self.reader.start()
        config = {"excludeUid": 4294967295, "userId": os.getuid() // 100000, "uids": [os.getuid()],
                  "packages": {}, "libraries": [str(apk)], "targets": []}
        self.process.stdin.write((json.dumps(config) + "\n").encode())
        self.process.stdin.flush()

    def _read(self):
        try:
            for line in self.process.stdout:
                self.queue.put(json.loads(line))
        except BaseException as error:
            self.queue.put(error)
        finally:
            self.queue.put(EOFError("collector exited"))

    def next(self, timeout=10):
        value = self.queue.get(timeout=timeout)
        if isinstance(value, BaseException):
            raise value
        assert value["type"] != "error", value.get("error")
        if value.get("id"):
            self.pids.add(value["pid"])
            self.flows.setdefault(value["id"], []).append(value)
        return value

    def ready(self):
        deadline = time.monotonic() + 25
        while time.monotonic() < deadline:
            value = self.next(max(.01, deadline - time.monotonic()))
            if value["type"] == "progress":
                continue
            assert value["type"] == "ready" and value["libraries"] == 1, value
            return
        raise AssertionError("APK probe preparation timed out")

    def expect(self, state, write=b"", read=b"", gap=False, pid=None):
        pointer = state if isinstance(state, int) else C.addressof(state)
        prefix = f"{os.getpid() if pid is None else pid}-{pointer:x}-"
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            for identity, events in self.flows.items():
                if identity in self.consumed or not identity.startswith(prefix) or events[-1]["type"] != "close":
                    continue
                assert events[0]["type"] == "open" and events[0].get("uid", 0) == os.getuid(), events
                assert sum(e["type"] == "open" for e in events) == 1, events
                assert sum(bool(e.get("gap")) for e in events) == int(gap), events
                for direction, expected in (("write", write), ("read", read)):
                    actual = b"".join(base64.b64decode(e.get("data", "")) for e in events if e.get("direction") == direction)
                    assert actual == expected, (direction, len(actual), len(expected), identity)
                self.consumed.add(identity)
                return
            self.next(max(.01, deadline - time.monotonic()))
        raise AssertionError(f"missing complete SSL lifetime for {prefix}")

    def stop(self):
        self.process.stdin.close()
        assert self.process.wait(timeout=5) == 0, "owner EOF failed to stop collector"
        self.reader.join(timeout=2)
        assert not self.reader.is_alive()
        while not self.queue.empty():
            try:
                self.next()
            except EOFError:
                break

    def close(self):
        if self.process.poll() is None:
            self.process.kill()
            self.process.wait(timeout=5)
        self.reader.join(timeout=2)
        self.errors.seek(0)
        diagnostic = self.errors.read().decode(errors="replace")
        if diagnostic:
            print(diagnostic, file=sys.stderr)
        self.errors.close()


def child_worker(apk, mode):
    library = MappedLibrary(apk)
    try:
        fixture = SSLFixture(library)
        if mode == "--excluded":
            os.setuid(65534)
        state = fixture.session()
        if mode == "--exited":
            print(json.dumps({"pid": os.getpid(), "pointer": C.addressof(state)}), flush=True)
            state.abandon = 2
        fixture.write(state, b"unselected uid must not be captured")
        fixture.free(C.byref(state))
    finally:
        library.close()


def exercise(apk, directory):
    library = MappedLibrary(apk)
    collector = Collector(apk, directory)
    fixture = SSLFixture(library)
    affinity = os.sched_getaffinity(0)
    blocked, worker, child = None, None, None
    try:
        collector.ready()
        # Existing mappings and probes must continue using this inode after an
        # installer replaces the public pathname with a different file.
        mapped_apk = apk.with_name("previous.apk")
        apk.rename(mapped_apk)
        apk.write_bytes(b"replacement package, not the mapped inode")
        cpus = sorted(affinity)
        assert len(cpus) >= 2, "cross-CPU smoke needs two permitted CPUs"
        os.sched_setaffinity(0, {cpus[0]})
        response = b"response-" * 1000
        state = fixture.session(response)
        fixture.write(state, b"normal-")
        os.sched_setaffinity(0, {cpus[-1]})
        fixture.write(state, b"nested-ex", "SSL_write_ex")
        received = fixture.read(state, 31)
        received += fixture.read(state, len(response), "SSL_read_ex")
        assert received == response
        fixture.free(C.byref(state))
        collector.expect(state, write=b"normal-nested-ex", read=response)

        state = fixture.session(b"lazy handshake response")
        state.abandon = 3  # SSL_clear nested in the first public I/O call
        fixture.write(state, b"lazy handshake request")
        assert fixture.read(state, 100) == b"lazy handshake response"
        fixture.free(C.byref(state))
        collector.expect(state, write=b"lazy handshake request", read=b"lazy handshake response")

        state = fixture.session()
        fixture.write(state, b"before-inflight-clear")
        state.abandon = 3
        fixture.write(state, b"cannot-migrate-an-inflight-call")
        fixture.write(state, b"must-not-restart-midstream")
        fixture.free(C.byref(state))
        collector.expect(state, write=b"before-inflight-clear", gap=True)

        state = fixture.session()
        boundary = b"b" * (128 * 1024)
        fixture.write(state, boundary, "SSL_write_ex")
        fixture.free(C.byref(state))
        collector.expect(state, write=boundary)

        state = fixture.session(b"early response")
        fixture.write(state, b"early request", "SSL_write_early_data")
        assert fixture.read(state, 100, "SSL_read_early_data") == b"early response"
        fixture.free(C.byref(state))
        collector.expect(state, write=b"early request", read=b"early response")

        state = fixture.session()
        fixture.write(state, b"before-clear")
        assert fixture.clear(C.byref(state)) == 1
        collector.expect(state, write=b"before-clear")
        fixture.write(state, b"after-clear")
        fixture.free(C.byref(state))
        collector.expect(state, write=b"after-clear")
        state = fixture.session()
        state.clear_result = 0
        assert fixture.clear(C.byref(state)) == 0
        collector.expect(state)
        fixture.write(state, b"failed SSL_clear must not create a generation")
        fixture.free(C.byref(state))

        state = fixture.session()
        fixture.write(state, boundary + b"overflow")
        fixture.free(C.byref(state))
        collector.expect(state, gap=True)

        # A skipped uretprobe must retire its old call/owner slot when another
        # SSL object starts an independent call on this thread.
        stale = fixture.session()
        assert fixture.abandon(C.byref(stale), C.create_string_buffer(b"abandoned"), 9) == 1
        healthy = fixture.session()
        fixture.write(healthy, b"healthy after skipped return")
        fixture.free(C.byref(stale))
        fixture.free(C.byref(healthy))
        collector.expect(stale, gap=True)
        collector.expect(healthy, write=b"healthy after skipped return")

        blocked = fixture.session()
        blocked.block = 1
        failures = []

        def simultaneous_write():
            try:
                os.sched_setaffinity(0, {cpus[0]})
                fixture.write(blocked, b"first writer")
            except BaseException as error:
                failures.append(error)

        worker = threading.Thread(target=simultaneous_write, daemon=True)
        worker.start()
        deadline = time.monotonic() + 5
        while not blocked.entered:
            assert time.monotonic() < deadline, "fixture write did not enter"
            time.sleep(.001)
        fixture.write(blocked, b"overlapping writer")
        blocked.block = 0
        worker.join(timeout=5)
        assert not worker.is_alive() and not failures, failures
        fixture.free(C.byref(blocked))
        collector.expect(blocked, gap=True)
        healthy = fixture.session()
        fixture.write(healthy, b"healthy after overlap")
        fixture.free(C.byref(healthy))
        collector.expect(healthy, write=b"healthy after overlap")

        exited = subprocess.run([sys.executable, str(Path(__file__).resolve()), "--exited", str(mapped_apk)],
                                check=True, stdout=subprocess.PIPE, text=True, timeout=10)
        interrupted = json.loads(exited.stdout)
        collector.expect(interrupted["pointer"], pid=interrupted["pid"], gap=True)

        child = subprocess.Popen([sys.executable, str(Path(__file__).resolve()), "--excluded", str(mapped_apk)])
        assert child.wait(timeout=10) == 0
        collector.stop()
        assert child.pid not in collector.pids, "BPF UID filter captured an excluded process"
        assert set(collector.flows) == collector.consumed, "unexpected SSL generation or data after retirement"
        print("Stored APK uprobes: original inode, automatic offsets, two CPUs, UID filtering, "
              "normal/_ex/early data, SSL_clear, local gaps, skipped returns and owner EOF passed.")
    finally:
        if blocked is not None:
            blocked.block = 0
        if worker is not None:
            worker.join(timeout=5)
        if child is not None and child.poll() is None:
            child.kill()
            child.wait(timeout=5)
        os.sched_setaffinity(0, affinity)
        collector.close()
        library.close()


def main():
    base = Path.home() / "tmp"
    base.mkdir(exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="bbox-apk-uprobe-", dir=base) as temporary:
        directory = Path(temporary)
        shared = directory / "fixture.so"
        subprocess.run(["clang", "-O1", "-shared", "-fPIC", "-nostdlib", "-fno-builtin", "-fno-stack-protector",
                        "-fno-optimize-sibling-calls", "-Wl,-Bsymbolic", "-Wl,-z,max-page-size=16384",
                        str(ROOT / "testdata/tls_fixture.c"), "-o", str(shared)], check=True)
        apk = directory / "base.apk"
        stored_apk(apk, shared.read_bytes())
        exercise(apk, directory)


if __name__ == "__main__":
    if len(sys.argv) == 3 and sys.argv[1] in ("--excluded", "--exited"):
        child_worker(Path(sys.argv[2]), sys.argv[1])
    else:
        main()
