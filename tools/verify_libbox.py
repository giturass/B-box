#!/usr/bin/env python3
"""Verify the actual Go build metadata of B-box's trimmed ARM64 libbox."""

import argparse
import io
import os
from pathlib import Path
import re
import shlex
import subprocess
import tempfile
from zipfile import ZipFile


REMOVED_TAGS = {
    "with_naive_outbound",
    "with_tailscale",
    "with_openvpn",
    "with_openconnect",
    "with_usbip",
    "with_embedded_tor",
}
REQUIRED_TAGS = {
    "with_quic",
    "with_wireguard",
    "with_utls",
    "with_clash_api",
    "badlinkname",
    "tfogo_checklinkname0",
}
REMOVED_MODULES = {
    "github.com/creack/pty",
    "github.com/sagernet/cronet-go",
    "github.com/sagernet/tailscale",
    "github.com/sagernet/sing-openvpn",
    "github.com/sagernet/sing-openconnect",
    "github.com/sagernet/sing-usbip",
    "github.com/sagernet/sing-snell",
    "github.com/cretz/bine",
}
REQUIRED_MODULES = {
    "github.com/sagernet/sing-box",
    "github.com/sagernet/quic-go",
    "github.com/metacubex/utls",
    "github.com/sagernet/wireguard-go",
}
REMOVED_PACKAGES = {
    "github.com/sagernet/sing-box/protocol/naive",
    "github.com/sagernet/sing-box/protocol/snell",
    "github.com/sagernet/sing-box/protocol/tor",
    "github.com/sagernet/sing-box/protocol/openvpn",
    "github.com/sagernet/sing-box/protocol/openconnect",
    "github.com/sagernet/sing-box/protocol/tailscale",
    "github.com/sagernet/sing-box/service/derp",
}


def verify_metadata(metadata: str) -> set[str]:
    modules = set()
    settings = {}
    for line in metadata.splitlines():
        fields = line.split(maxsplit=2)
        if len(fields) >= 2 and fields[0] in {"mod", "dep", "=>"}:
            modules.add(fields[1])
        elif len(fields) >= 2 and fields[0] == "build":
            values = shlex.split(line.strip()[len("build"):].strip())
            if len(values) != 1 or "=" not in values[0]:
                raise ValueError(f"Unexpected Go build setting: {line!r}")
            key, value = values[0].split("=", 1)
            settings[key] = value

    tags = set(settings.get("-tags", "").replace(",", " ").split())
    removed = tags & REMOVED_TAGS
    if removed:
        raise ValueError(f"Removed features are enabled: {sorted(removed)}")
    missing = REQUIRED_TAGS - tags
    if missing:
        raise ValueError(f"Required features are missing: {sorted(missing)}")
    included = {
        module for module in modules
        if any(module == removed or module.startswith(removed + "/")
               for removed in REMOVED_MODULES)
    }
    if included:
        raise ValueError(f"Removed dependencies are still linked: {sorted(included)}")
    missing = REQUIRED_MODULES - modules
    if missing:
        raise ValueError(f"Required dependencies are missing: {sorted(missing)}")
    for key, value in {"GOOS": "android", "GOARCH": "arm64"}.items():
        if settings.get(key) != value:
            raise ValueError(f"Expected {key}={value}, found {settings.get(key)!r}")
    return tags


def verify_source(source: Path, tags: set[str]) -> None:
    environment = os.environ.copy()
    environment.update(GOOS="android", GOARCH="arm64", CGO_ENABLED="1")
    output = subprocess.check_output(
        ["go", "list", "-deps", "-tags", ",".join(sorted(tags)), "./experimental/libbox"],
        cwd=source, env=environment, text=True,
    )
    packages = set(output.splitlines())
    included = {
        package for package in packages
        if any(package == removed or package.startswith(removed + "/")
               for removed in REMOVED_PACKAGES | REMOVED_MODULES)
    }
    if included:
        raise ValueError(f"Removed implementations are still imported: {sorted(included)}")
    print(f"Verified Android libbox dependency graph: {len(packages)} packages")


def verify_native(data: bytes) -> None:
    included = {
        package for package in REMOVED_PACKAGES | REMOVED_MODULES
        if re.search(re.escape(package.encode()) + rb"[./\x00]", data)
    }
    if included:
        raise ValueError(f"Removed implementations remain in native library: {sorted(included)}")


def verify_bindings(aar: ZipFile) -> None:
    with ZipFile(io.BytesIO(aar.read("classes.jar"))) as classes:
        removed = [
            name for name in classes.namelist()
            if name.startswith(("io/nekohasekai/libbox/Tailscale", "io/nekohasekai/libbox/Taildrop"))
            and name.endswith(".class")
        ]
        libbox = classes.read("io/nekohasekai/libbox/Libbox.class")
        removed.extend(
            method.decode() for method in (b"openNativeShellSession", b"openNativePipeSession")
            if method in libbox
        )
    if removed:
        raise ValueError(f"Removed Java bindings are still exported: {sorted(removed)}")
    print(f"Verified trimmed Java bindings: {aar.filename}")


def verify_entry(archive: ZipFile, entry: str, directory: Path) -> set[str]:
    # Use a fixed filename rather than extracting paths supplied by the archive.
    native = directory / "libbox.so"
    data = archive.read(entry)
    verify_native(data)
    native.write_bytes(data)
    metadata = subprocess.check_output(
        ["go", "version", "-m", str(native)], text=True,
    )
    tags = verify_metadata(metadata)
    info = archive.getinfo(entry)
    print(
        f"Verified {archive.filename}: {entry}, "
        f"native {info.file_size:,} bytes, ZIP entry {info.compress_size:,} bytes"
    )
    return tags


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("aar", type=Path)
    parser.add_argument("--source", type=Path, help="Also inspect the Android Go dependency graph")
    parser.add_argument("--apk", type=Path, help="Also verify the packaged native library")
    arguments = parser.parse_args()
    temporary_root = Path.home() / "tmp"
    temporary_root.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="verify-libbox-", dir=temporary_root) as temp:
        directory = Path(temp)
        with ZipFile(arguments.aar) as aar:
            verify_bindings(aar)
            native_entries = [
                name for name in aar.namelist()
                if name.startswith("jni/") and name.endswith(".so")
            ]
            if len(native_entries) != 1 or not native_entries[0].startswith("jni/arm64-v8a/"):
                raise ValueError(f"Expected one ARM64 libbox library, found {native_entries}")
            entry = native_entries[0]
            tags = verify_entry(aar, entry, directory)
            apk_entry = "lib/arm64-v8a/" + Path(entry).name
        if arguments.source:
            verify_source(arguments.source, tags)
        if arguments.apk:
            with ZipFile(arguments.apk) as apk:
                verify_entry(apk, apk_entry, directory)


if __name__ == "__main__":
    main()
