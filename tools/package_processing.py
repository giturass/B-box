"""Embed the processor for exactly the ABI shipped by the libbox AAR."""

import os
import re
import shutil
import struct
import subprocess
import sys
import tempfile
import zipfile
from pathlib import Path

TARGETS = {
    "android/arm64": ("arm64-v8a", 2, 183),
    "android/arm": ("armeabi-v7a", 1, 40),
    "android/amd64": ("x86_64", 2, 62),
}
EXPORTS = {f"concord_processing_{operation}" for operation in
           ("abi", "open", "call", "serve", "close", "free")}


def verify_elf(data: bytes, platform: str, label: str):
    _, elf_class, machine = TARGETS[platform]
    if len(data) < 64 or data[:4] != b"\x7fELF" or data[4] != elf_class or data[5] != 1:
        raise SystemExit(f"{label}: expected a little-endian ELF for {platform}")
    if struct.unpack_from("<H", data, 18)[0] != machine:
        raise SystemExit(f"{label}: ELF machine does not match {platform}")
    if elf_class == 2:
        offset = struct.unpack_from("<Q", data, 32)[0]
        entry_size, count = struct.unpack_from("<HH", data, 54)
        minimum_size, address_offset, alignment_offset, word = 56, 16, 48, "<Q"
    else:
        offset = struct.unpack_from("<I", data, 28)[0]
        entry_size, count = struct.unpack_from("<HH", data, 42)
        minimum_size, address_offset, alignment_offset, word = 32, 8, 28, "<I"
    if not count or entry_size < minimum_size or offset + entry_size * count > len(data):
        raise SystemExit(f"{label}: invalid ELF program headers")
    loads = 0
    for index in range(count):
        header = offset + index * entry_size
        if struct.unpack_from("<I", data, header)[0] != 1:
            continue
        loads += 1
        alignment = struct.unpack_from(word, data, header + alignment_offset)[0]
        address = struct.unpack_from(word, data, header + address_offset)[0]
        file_offset = struct.unpack_from(word, data, header + (8 if elf_class == 2 else 4))[0]
        if alignment < 16384 or (address - file_offset) % 16384:
            raise SystemExit(f"{label}: LOAD segment does not support Android's 16 KiB pages")
    if not loads:
        raise SystemExit(f"{label}: ELF has no LOAD segments")


def package(aar: Path, library: Path, platform: str):
    abi, _, _ = TARGETS[platform]
    verify_elf(library.read_bytes(), platform, "Concord")
    ndk = Path(os.environ["ANDROID_HOME"]) / "ndk/28.2.13676358"
    readelf = ndk / "toolchains/llvm/prebuilt/linux-x86_64/bin/llvm-readelf"
    dynamic = subprocess.check_output([readelf, "-d", library], text=True)
    if "libc++_shared.so" in dynamic:
        raise SystemExit("Concord must use the pinned NDK's static C++ runtime")
    symbols = subprocess.check_output([readelf, "--dyn-syms", "--wide", library], text=True)
    exported = {line.split()[-1] for line in symbols.splitlines()
                if re.search(r"\bGLOBAL\b", line) and " UND " not in line and line.split()}
    if not EXPORTS.issubset(exported):
        raise SystemExit(f"Concord C ABI exports are missing: {sorted(EXPORTS - exported)}")
    with zipfile.ZipFile(aar) as original:
        cores = {name.split("/")[1] for name in original.namelist()
                 if name.startswith("jni/") and name.endswith("/libbox.so")}
        if cores != {abi}:
            raise SystemExit(f"libbox ABIs {cores} do not match Concord's {abi}")
        verify_elf(original.read(f"jni/{abi}/libbox.so"), platform, "libbox")
        processing_path = f"jni/{abi}/libconcord_processing.so"
        fd, temporary = tempfile.mkstemp(dir=aar.parent, suffix=".aar")
        os.close(fd)
        try:
            with zipfile.ZipFile(temporary, "w", compression=zipfile.ZIP_DEFLATED) as output:
                for entry in original.infolist():
                    if entry.filename != processing_path:
                        output.writestr(entry, original.read(entry.filename))
                output.write(library, processing_path)
            shutil.move(temporary, aar)
        finally:
            Path(temporary).unlink(missing_ok=True)


if __name__ == "__main__":
    package(Path(sys.argv[1]), Path(sys.argv[2]), sys.argv[3])
