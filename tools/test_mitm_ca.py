"""Linux namespace regression, run as PID 1 in an isolated GitHub Actions job.

Uses the shipped shell scripts and real tmpfs/bind mounts in disposable Android
filesystem fixtures. Ubuntu's BusyBox omits flock, so the fixture also includes
util-linux's real flock. It does not install a CA on the runner or Android.
"""
import contextlib
import ctypes
import json
import os
from pathlib import Path
import shutil
import signal
import socket
import stat
import subprocess
import tempfile
import time
import unittest


REPO = Path(__file__).resolve().parents[1]
MODULE = "/data/adb/modules/bbox_mitm_ca"
SYSTEM = "/system/etc/security/cacerts"
APEX = "/apex/com.android.conscrypt/cacerts"
HASH = "01234567"
CA = b"B-box public certificate fixture\n"
LIBC = ctypes.CDLL(None, use_errno=True)


def mount(source, target, kind=None, flags=4096):
    def encode(value):
        return os.fsencode(value) if value is not None else None
    if LIBC.mount(encode(source), encode(target), encode(kind), ctypes.c_ulong(flags), None):
        raise OSError(ctypes.get_errno(), f"mount {source} -> {target}")


def private_namespace():
    if LIBC.unshare(0x00020000):  # CLONE_NEWNS
        raise OSError(ctypes.get_errno(), "unshare")
    mount(None, "/", flags=16384 | (1 << 18))  # MS_REC | MS_PRIVATE


def actor_setup(name, stores=None, hide=False):
    private_namespace()
    if LIBC.prctl(15, name.encode(), 0, 0, 0):  # PR_SET_NAME, used by pidof
        raise OSError(ctypes.get_errno(), "prctl")
    if stores:
        mount(f"/fixtures/{stores}/system", SYSTEM)
        mount(f"/fixtures/{stores}/apex", APEX)
    if hide:
        mount("tmpfs", "/data/adb/modules", "tmpfs", 0)


class MountTests(unittest.TestCase):
    def setUp(self):
        scratch = Path.home() / "tmp"
        scratch.mkdir(exist_ok=True)
        self.temporary = tempfile.TemporaryDirectory(prefix="bbox-ca-", dir=scratch)
        self.root = Path(self.temporary.name) / "root"
        self.root.mkdir()
        mount(self.root, self.root)
        self.processes = []
        self.channels = []
        for name in ("bin", "dev", "proc", "properties", "system/bin", MODULE[1:], SYSTEM[1:], APEX[1:]):
            (self.root / name).mkdir(parents=True, exist_ok=True)
        mount("/proc", self.root / "proc")
        os.mknod(self.root / "dev/null", stat.S_IFCHR | 0o666, os.makedev(1, 3))
        shutil.copy2(shutil.which("busybox"), self.root / "bin/busybox")
        self.run_in_root("/bin/busybox", "--install", "-s", "/bin")
        self.install_host_tool("flock")
        (self.root / "system/bin/sh").symlink_to("/bin/sh")
        self.script("getprop", 'case "$1" in ro.build.version.sdk) cat /properties/sdk;; sys.boot_completed) cat /properties/boot;; esac')
        self.script("getenforce", "cat /properties/enforce")
        self.property("sdk", "34")
        self.property("boot", "1")
        self.property("enforce", "Disabled")
        self.module = self.root / MODULE[1:]
        shutil.copytree(REPO / "app/src/main/assets/mitm-ca", self.module, dirs_exist_ok=True)
        (self.module / "module.prop").write_text("id=bbox_mitm_ca\n")
        (self.module / "cacert").mkdir()
        (self.module / f"cacert/{HASH}.0").write_bytes(CA)
        self.stores("base")
        mount(self.root / "fixtures/base/system", self.root / SYSTEM[1:])
        mount(self.root / "fixtures/base/apex", self.root / APEX[1:])

    def tearDown(self):
        for pid in self.processes:
            with contextlib.suppress(ProcessLookupError):
                os.killpg(pid, signal.SIGKILL)
            with contextlib.suppress(ChildProcessError):
                os.waitpid(pid, 0)
        for channel in self.channels:
            channel.close()
        subprocess.run(["umount", "-R", str(self.root)], check=True)
        self.temporary.cleanup()

    def script(self, name, body):
        path = self.root / "bin" / name
        # BusyBox may have installed an absolute applet symlink. Replace it in
        # the fixture instead of following it from the controller's root.
        path.unlink(missing_ok=True)
        path.write_text(f"#!/system/bin/sh\n{body}\n")
        path.chmod(0o755)

    def install_host_tool(self, name):
        # Ubuntu 24.04 busybox-static has CONFIG_FLOCK disabled. Keep real
        # kernel file locking in the chroot, including its dynamic loader.
        source = shutil.which(name)
        self.assertIsNotNone(source, f"Missing runner tool: {name}")
        target = self.root / "bin" / name
        target.unlink(missing_ok=True)
        shutil.copy2(source, target)
        dependencies = subprocess.run(["ldd", source], text=True, capture_output=True,
                                      check=True, timeout=10)
        for path in dependencies.stdout.split():
            if not path.startswith("/"):
                continue
            target = self.root / path[1:]
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(path, target)

    def property(self, name, value):
        (self.root / "properties" / name).write_text(value + "\n")

    def stores(self, name):
        for store in ("system", "apex"):
            directory = self.root / "fixtures" / name / store
            directory.mkdir(parents=True)
            (directory / f"{HASH}.0").write_text(f"{name}-{store}-collision\n")
            (directory / "aaaaaaaa.0").write_text(f"{name}-{store}-original\n")

    def run_in_root(self, *args, check=True):
        return subprocess.run(["chroot", str(self.root), *args], text=True,
                              stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                              check=check, timeout=15)

    def inject(self, mode="--apps", expected=0):
        result = self.run_in_root("/bin/busybox", "sh", f"{MODULE}/inject.sh", mode, check=False)
        self.assertEqual(expected, result.returncode, result.stdout)
        return result.stdout

    def actor(self, name="zygote", stores=None, hide=False):
        parent, child = socket.socketpair()
        pid = os.fork()
        if pid == 0:
            parent.close()
            os.setsid()
            try:
                os.chroot(self.root)
                os.chdir("/")
                actor_setup(name, stores, hide)
                channel = child.makefile("rwb", buffering=0)
                channel.write(b'{"ready":true}\n')
                for line in channel:
                    command = json.loads(line)
                    if command["op"] == "fork":
                        read_fd, write_fd = os.pipe()
                        descendant = os.fork()
                        if descendant == 0:
                            os.close(read_fd)
                            for _ in range(command.get("depth", 1) - 1):
                                LIBC.prctl(15, b"app_zygote", 0, 0, 0)
                                if os.fork() != 0:
                                    os.close(write_fd)
                                    child.close()
                                    channel.close()
                                    while True:
                                        signal.pause()
                            actor_setup(command.get("name", "isolated_app"), command.get("stores"), command.get("hide", False))
                            os.write(write_fd, str(os.getpid()).encode())
                            os.close(write_fd)
                            child.close()
                            channel.close()
                            while True:
                                signal.pause()
                        os.close(write_fd)
                        descendant = int(os.read(read_fd, 32))
                        os.close(read_fd)
                        reply = {"pid": descendant}
                    elif command["op"] == "remount":
                        for target, store in ((SYSTEM, "system"), (APEX, "apex")):
                            mount(f'/fixtures/{command["stores"]}/{store}', target)
                        reply = {"ready": True}
                    else:
                        raise ValueError(command)
                    channel.write(json.dumps(reply).encode() + b"\n")
            except BaseException as error:
                with contextlib.suppress(Exception):
                    child.sendall(json.dumps({"error": repr(error)}).encode() + b"\n")
                os._exit(1)
            os._exit(0)
        child.close()
        self.processes.append(pid)
        parent.settimeout(10)
        channel = parent.makefile("rwb", buffering=0)
        parent.close()
        self.channels.append(channel)
        self.assertEqual({"ready": True}, json.loads(channel.readline()))
        return pid, channel

    def request(self, actor, **command):
        actor[1].write(json.dumps(command).encode() + b"\n")
        reply = json.loads(actor[1].readline())
        self.assertNotIn("error", reply)
        return reply

    def view(self, target, pid=None):
        return (Path(f"/proc/{pid}/root") if pid else self.root) / target[1:]

    def assert_store(self, target, original, pid=None):
        directory = self.view(target, pid)
        self.assertEqual(CA, (directory / f"{HASH}.1").read_bytes())
        self.assertEqual(original + "-collision\n", (directory / f"{HASH}.0").read_text())
        self.assertEqual(original + "-original\n", (directory / "aaaaaaaa.0").read_text())
        self.assertEqual(3, len(list(directory.iterdir())))
        self.assertEqual(0o644, (directory / f"{HASH}.1").stat().st_mode & 0o777)

    def wait_for(self, check, timeout=40):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if check():
                return
            time.sleep(0.1)
        diagnostics = []
        for name in ("mount.status", "mount.log"):
            path = self.module / name
            diagnostics.append(f"{name}:\n{path.read_text() if path.exists() else '(missing)'}")
        self.fail("timed out waiting for namespace reconciliation\n" + "\n".join(diagnostics))

    def remove_apex(self):
        subprocess.run(["umount", str(self.root / APEX[1:])], check=True)
        (self.root / APEX[1:]).rmdir()

    def test_early_missing_apex_is_pending_and_retry_preserves_collisions(self):
        self.remove_apex()
        output = self.inject("--early", expected=2)
        self.assertIn("pending:", output)
        self.assert_store(SYSTEM, "base-system")
        (self.root / APEX[1:]).mkdir()
        mount(self.root / "fixtures/base/apex", self.root / APEX[1:])
        self.inject("--early")
        self.assert_store(APEX, "base-apex")
        self.assertNotIn("mounted:", self.inject("--early"))
        self.assertFalse(list((self.root / "dev").glob(".bbox-ca.*")))
        self.assertEqual(2, len(list((self.root / "fixtures/base/system").iterdir())))

    def test_pre_android14_apex_is_optional(self):
        self.property("sdk", "24")
        self.remove_apex()
        self.inject("--early")
        self.assert_store(SYSTEM, "base-system")

    def test_existing_grandchild_and_hidden_module_namespace(self):
        self.stores("zygote")
        self.stores("grandchild")
        zygote = self.actor(stores="zygote")
        descendant = self.request(zygote, op="fork", stores="grandchild", hide=True, depth=2)["pid"]
        self.assertIn(str(zygote[0]), self.run_in_root("/bin/busybox", "pidof", "zygote").stdout.split())
        self.inject()
        for target, store in ((SYSTEM, "system"), (APEX, "apex")):
            self.assert_store(target, "zygote-" + store, zygote[0])
            self.assert_store(target, "grandchild-" + store, descendant)
        # A child forked after its parent is patched inherits both stores.
        inherited = self.request(zygote, op="fork")["pid"]
        self.assert_store(APEX, "zygote-apex", inherited)
        self.assertNotIn("mounted:", self.inject())

    def test_remount_in_same_namespace_is_not_cached_as_success(self):
        self.stores("replacement")
        zygote = self.actor()
        identity = os.readlink(f"/proc/{zygote[0]}/ns/mnt")
        self.inject()
        self.request(zygote, op="remount", stores="replacement")
        self.assertEqual(identity, os.readlink(f"/proc/{zygote[0]}/ns/mnt"))
        self.inject()
        self.assert_store(APEX, "replacement-apex", zygote[0])
        self.assert_store(SYSTEM, "replacement-system", zygote[0])

    def test_preparation_failure_is_reported_without_replacing_stores(self):
        self.property("enforce", "Unknown")
        self.assertIn("failed:", self.inject("--early", expected=1))
        for target in (SYSTEM, APEX):
            self.assertEqual(2, len(list(self.view(target).iterdir())))
        self.assertFalse(list((self.root / "dev").glob(".bbox-ca.*")))
        self.property("enforce", "Disabled")
        self.inject("--early")

    def test_service_retries_boot_and_late_namespaces_until_disabled(self):
        self.remove_apex()
        self.property("boot", "0")
        service = subprocess.Popen(["chroot", str(self.root), "/bin/busybox", "sh", f"{MODULE}/service.sh"], start_new_session=True)
        self.processes.append(service.pid)
        status = self.module / "mount.status"
        self.wait_for(lambda: status.exists() and "pending:" in status.read_text(), timeout=10)
        (self.root / APEX[1:]).mkdir()
        mount(self.root / "fixtures/base/apex", self.root / APEX[1:])
        zygote = self.actor()
        self.property("boot", "1")
        self.wait_for(lambda: "exit=0" in status.read_text(), timeout=15)
        self.stores("late")
        self.stores("remounted")
        late = self.actor(name="native_worker", stores="late")
        self.request(zygote, op="remount", stores="remounted")
        self.wait_for(lambda: (self.view(APEX, late[0]) / f"{HASH}.1").exists() and
                      (self.view(APEX, zygote[0]) / f"{HASH}.1").exists())
        self.assert_store(APEX, "late-apex", late[0])
        self.assert_store(APEX, "remounted-apex", zygote[0])
        # A second service entry must not start a second reconciliation loop.
        self.run_in_root("/bin/busybox", "sh", f"{MODULE}/service.sh")
        (self.module / "disable").touch()
        self.assertEqual(0, service.wait(timeout=35))
        self.assertLessEqual((self.module / "mount.log").stat().st_size, 65536)


if __name__ == "__main__":
    if os.environ.get("GITHUB_ACTIONS") != "true" or os.geteuid() != 0 or os.getpid() != 1:
        raise SystemExit("Run only in Actions: sudo --preserve-env=GITHUB_ACTIONS unshare --mount --pid --fork --mount-proc python3 tools/test_mitm_ca.py")
    mount(None, "/", flags=16384 | (1 << 18))
    unittest.main(verbosity=2)
