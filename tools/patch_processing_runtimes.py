"""Install cancellation support into isolated copies of the pinned JS runtimes.

Run only in the shared Actions core setup, after pinning module versions.
The module cache stays unchanged. A version change requires rebasing the patches.
"""

import argparse
import json
import shutil
import stat
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
RUNTIMES = (
    ("github.com/dlclark/regexp2/v2", "v2.11.0", "regexp2", "regexp2-cancel.patch"),
    ("github.com/dop251/goja", "v0.0.0-20261102223426-a177d211bf60", "goja", "goja-regexp-cancel.patch"),
)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("core", type=Path)
    core = parser.parse_args().core.resolve()
    for module, version, name, patch_name in RUNTIMES:
        dependency = json.loads(subprocess.check_output(
            ["go", "mod", "download", "-json", module], cwd=core, text=True,
        ))
        if dependency.get("Path") != module or dependency.get("Version") != version:
            raise SystemExit(f"Rebase {patch_name} before changing {module}@{version}")
        target = core / ".b-box" / name
        shutil.copytree(dependency["Dir"], target)
        for path in [target, *target.rglob("*")]:
            path.chmod(path.stat().st_mode | stat.S_IWUSR)
        patch = ROOT / "libbox" / patch_name
        for arguments in (["--check"], []):
            subprocess.run(
                ["git", "apply", f"--directory=.b-box/{name}", *arguments, str(patch)],
                cwd=core, check=True,
            )
        for source in (ROOT / "libbox" / name).glob("*.go"):
            shutil.copyfile(source, target / source.name)
        subprocess.run(
            ["go", "mod", "edit", f"-replace={module}=./.b-box/{name}"], cwd=core, check=True,
        )
        print(f"Applied request/VM cancellation support to {module}@{version}")


if __name__ == "__main__":
    main()
