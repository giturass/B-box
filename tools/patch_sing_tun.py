"""Apply B-box's Android ingress-map fix to an isolated sing-tun dependency.

Run after setting up the matching Go toolchain, before testing/building libbox.
The shared Go module cache is never modified.
"""

import argparse
import json
import shutil
import stat
import subprocess
from pathlib import Path

MODULE = "github.com/sagernet/sing-tun"
VERSION = "v0.9.7-0.20261023115453-611d83b7a57b"
ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("core", type=Path, help="matching sing-box checkout")
    core = parser.parse_args().core.resolve()
    dependency = json.loads(subprocess.check_output(
        ["go", "mod", "download", "-json", MODULE], cwd=core, text=True,
    ))
    if dependency.get("Path") != MODULE or dependency.get("Version") != VERSION:
        raise SystemExit(
            f"Rebase sing-tun-android-ingress.patch before changing {MODULE}: "
            f"expected {VERSION}, got {dependency.get('Version')}"
        )

    target = core / ".b-box" / "sing-tun"
    # Refuse an existing copy instead of silently discarding local changes.
    shutil.copytree(dependency["Dir"], target)
    for path in [target, *target.rglob("*")]:
        path.chmod(path.stat().st_mode | stat.S_IWUSR)

    patch = ROOT / "libbox/sing-tun-android-ingress.patch"
    for arguments in (["--check"], []):
        subprocess.run(
            ["git", "apply", "--directory=.b-box/sing-tun", *arguments, str(patch)],
            cwd=core, check=True,
        )
    shutil.copyfile(
        ROOT / "libbox/sing-tun/redirect_ingress_discard_linux_test.go",
        target / "redirect_ingress_discard_value_linux_test.go",
    )
    subprocess.run(
        ["go", "mod", "edit", f"-replace={MODULE}=./.b-box/sing-tun"],
        cwd=core, check=True,
    )
    print(f"Applied Android ingress-map compatibility fix to {MODULE}@{VERSION}")


if __name__ == "__main__":
    main()
