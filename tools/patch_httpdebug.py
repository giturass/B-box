"""Install B-box's capture engine and bindings into the matching core checkout."""

import argparse
import shutil
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SCRIPT_MODULE = "github.com/dop251/goja@v0.0.0-20250630131328-58d95d85e994"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("core", type=Path)
    core = parser.parse_args().core.resolve()
    patch = ROOT / "libbox/httpdebug.patch"
    targets = [core / "experimental/httpdebug", core / "route/b_box_httpdebug.go",
               core / "experimental/libbox/ui_capture.go"]
    if any(path.exists() for path in targets):
        raise SystemExit("Capture overlay already exists; use a clean matching core checkout")
    subprocess.run(["git", "apply", "--check", str(patch)], cwd=core, check=True)
    subprocess.run(["git", "apply", str(patch)], cwd=core, check=True)
    shutil.copytree(ROOT / "libbox/httpdebug", targets[0])
    for source in (ROOT / "libbox/route").glob("*.go"):
        shutil.copyfile(source, core / "route" / ("b_box_" + source.name))
    shutil.copyfile(ROOT / "libbox/ui_capture.go", targets[2])
    # This pinned pure-Go runtime supports Android without a second native
    # engine. go get resolves its sums without upgrading unrelated core modules.
    subprocess.run(["go", "get", SCRIPT_MODULE], cwd=core, check=True)
    print("Installed the HTTP debugging route hooks, engine and libbox bindings")


if __name__ == "__main__":
    main()
