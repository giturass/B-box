"""Check actual module metadata and DEX definitions in debug and minified APKs."""

import struct
import sys
from pathlib import Path
from zipfile import ZipFile


def defined_classes(data):
    if not data.startswith(b"dex\n"):
        raise ValueError("Unsupported DEX header")

    def uint(offset):
        return struct.unpack_from("<I", data, offset)[0]

    def string(index):
        offset = uint(uint(60) + 4 * index)
        # The ULEB128 UTF-16 length precedes the null-terminated descriptor.
        while data[offset] & 0x80:
            offset += 1
        offset += 1
        return data[offset:data.index(0, offset)].decode("utf-8")

    types = uint(68)
    classes = uint(100)
    return {string(uint(types + 4 * uint(classes + 32 * i))) for i in range(uint(96))}


def verify_xposed_apk(path):
    metadata = Path(__file__).resolve().parents[1] / "app/src/main/resources/META-INF/xposed"
    required = {
        "Lio/nekohasekai/sfa/xposed/XposedInit;",
        "Lio/nekohasekai/sfa/xposed/XposedInit101;",
        "Lio/nekohasekai/sfa/xposed/hooks/SafeMethodHook;",
        "Lio/github/libxposed/service/XposedProvider;",
        "Lio/nekohasekai/sfa/bg/LogEntry;",
        "Lio/nekohasekai/sfa/bg/PackageEntry;",
    }
    classes = set()
    with ZipFile(path) as apk:
        for name in ("java_init.list", "module.prop", "scope.list"):
            if apk.read("META-INF/xposed/" + name) != (metadata / name).read_bytes():
                raise ValueError(f"Changed LSPosed metadata: {name}")
        for name in apk.namelist():
            if name.startswith("classes") and name.endswith(".dex"):
                classes.update(defined_classes(apk.read(name)))
    missing = required - classes
    bundled_api = {name for name in classes if name.startswith(("Lio/github/libxposed/api/", "Lde/robv/android/xposed/"))}
    if missing or bundled_api:
        raise ValueError(f"Invalid LSPosed packaging: missing={sorted(missing)}, bundled framework API={sorted(bundled_api)}")
    print(f"Verified LSPosed metadata, entry points and compile-only API exclusion: {path}")


if __name__ == "__main__":
    if len(sys.argv) < 2:
        raise SystemExit("Usage: verify_xposed.py APK [APK ...]")
    for argument in sys.argv[1:]:
        verify_xposed_apk(Path(argument))
