"""Prepare the pinned cilium/ebpf Android fix without modifying the module cache.

The generated dependency and alternate module files live in build/ and are
refreshed before each collector build. The checked-in go.mod/go.sum stay intact.
"""

import json
import shutil
import stat
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parent
REPOSITORY = ROOT.parents[1]
MODULE = "github.com/cilium/ebpf"
VERSION = "v0.22.0"


def main():
    dependency = json.loads(subprocess.check_output(
        ["go", "mod", "download", "-modfile=go.mod", "-json", MODULE], cwd=ROOT, text=True,
    ))
    if dependency.get("Path") != MODULE or dependency.get("Version") != VERSION:
        raise SystemExit(
            f"Rebase cilium-ebpf-android.patch before changing {MODULE}: "
            f"expected {VERSION}, got {dependency.get('Version')}"
        )

    target = ROOT / "build/cilium-ebpf"
    shutil.copytree(dependency["Dir"], target, dirs_exist_ok=True)
    for path in [target, *target.rglob("*")]:
        path.chmod(path.stat().st_mode | stat.S_IWUSR)
    patch = ROOT / "cilium-ebpf-android.patch"
    for arguments in (["--check"], []):
        subprocess.run(
            ["git", "apply", f"--directory={target.relative_to(REPOSITORY)}",
             *arguments, str(patch)], cwd=REPOSITORY, check=True,
        )

    # Keep these tests inside the dependency so they can also exercise its
    # internal platform/version selection, without importing internal APIs in
    # the production collector.
    tests = target / "bboxtest"
    tests.mkdir(exist_ok=True)
    shutil.copyfile(ROOT / "testdata/platform_test.go", tests / "platform_test.go")

    shutil.copyfile(ROOT / "go.mod", ROOT / "build/capture.mod")
    shutil.copyfile(ROOT / "go.sum", ROOT / "build/capture.sum")
    subprocess.run(
        ["go", "mod", "edit", "-modfile=build/capture.mod",
         f"-replace={MODULE}@{VERSION}=./build/cilium-ebpf"], cwd=ROOT, check=True,
    )
    print(f"Prepared Android platform compatibility for {MODULE}@{VERSION}")


if __name__ == "__main__":
    main()
