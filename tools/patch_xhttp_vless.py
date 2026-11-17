"""Install the pinned XHTTP and VLESS Encryption overlay into B-box's core."""

import argparse
import hashlib
import json
import shutil
import stat
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def git(core, *arguments):
    return subprocess.check_output(["git", "-C", str(core), *arguments])


def sha256(content):
    return hashlib.sha256(content).hexdigest()


def install_vision_pointer_compatibility(core, manifest):
    dependency_spec = manifest["dependencies"][0]
    module = dependency_spec["module"]
    version = dependency_spec["version"]
    dependency = json.loads(subprocess.check_output(
        ["go", "mod", "download", "-json", module], cwd=core, text=True,
    ))
    if dependency.get("Path") != module or dependency.get("Version") != version:
        raise SystemExit(f"Rebase the Vision pointer patch before changing {module}@{version}")
    original = Path(dependency["Dir"])
    for entry in dependency_spec["files"]:
        if sha256((original / entry["path"]).read_bytes()) != entry["sha256"]:
            raise SystemExit("Unexpected pinned sing-vmess source: " + entry["path"])
    patch = ROOT / dependency_spec["patch"]
    if sha256(patch.read_bytes()) != dependency_spec["patch_sha256"]:
        raise SystemExit("Vision pointer patch hash differs from the manifest")
    for entry in dependency_spec["tests"]:
        if sha256((ROOT / entry["source"]).read_bytes()) != entry["sha256"]:
            raise SystemExit("Vision pointer regression hash differs from the manifest")
    # Patch an isolated copy of the current SagerNet module. Its version and
    # source stay fixed; the shared module cache is never made writable.
    target = core / ".b-box" / "sing-vmess"
    if target.exists():
        raise SystemExit("Vision dependency overlay already exists; use a clean matching checkout")
    shutil.copytree(original, target)
    for path in [target, *target.rglob("*")]:
        path.chmod(path.stat().st_mode | stat.S_IWUSR)
    for arguments in (["--check"], []):
        subprocess.run(
            ["git", "apply", "--directory=.b-box/sing-vmess", *arguments, str(patch)],
            cwd=core, check=True,
        )
    for entry in dependency_spec["tests"]:
        shutil.copyfile(ROOT / entry["source"], target / entry["target"])
    subprocess.run(
        ["go", "mod", "edit", f"-replace={module}=./.b-box/sing-vmess"], cwd=core, check=True,
    )


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("core", type=Path, help="matching sing-box checkout")
    core = parser.parse_args().core.resolve()
    source = ROOT / "libbox/xhttp"
    manifest = json.loads((source / "manifest.json").read_text())
    if git(core, "rev-parse", "HEAD").decode().strip() != manifest["upstream_commit"]:
        raise SystemExit("XHTTP/VLESS overlay requires the pinned core commit; review it when updating the core")
    # Read pristine HEAD blobs: legacy and other independent B-box patches may
    # already be installed in the working tree. git apply checks that state.
    for entry in manifest["patch_sources"]:
        if sha256(git(core, "show", "HEAD:" + entry["path"])) != entry["upstream_sha256"]:
            raise SystemExit("Unexpected upstream source: " + entry["path"])
    files = manifest["files"]
    if {p.relative_to(source).as_posix() for p in source.rglob("*.go")} != {entry["target"] for entry in files}:
        raise SystemExit("XHTTP/VLESS manifest does not cover every Go overlay file")
    for entry in files:
        target = core / entry["target"]
        if target.exists():
            raise SystemExit("XHTTP/VLESS overlay already exists; use a clean matching checkout: " + entry["target"])
        if sha256((source / entry["target"]).read_bytes()) != entry["overlay_sha256"]:
            raise SystemExit("XHTTP/VLESS source hash differs from the manifest: " + entry["target"])
    patch = ROOT / "libbox/xhttp-vless.patch"
    if sha256(patch.read_bytes()) != manifest["patch_sha256"]:
        raise SystemExit("XHTTP/VLESS patch hash differs from the manifest")
    subprocess.run(["git", "apply", "--check", str(patch)], cwd=core, check=True)
    install_vision_pointer_compatibility(core, manifest)
    subprocess.run(["git", "apply", str(patch)], cwd=core, check=True)
    for entry in files:
        target = core / entry["target"]
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source / entry["target"], target)
    print("Installed pinned XHTTP, VLESS Encryption, pointer-safe Vision, TLS adapters and regression tests")


if __name__ == "__main__":
    main()
