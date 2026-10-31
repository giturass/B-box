"""Run the standalone capture-engine tests with low CPU and memory concurrency.

Requires explicit authorization for local testing. All caches and generated
files stay in ~/tmp. The Android/libbox integration is covered by CI separately.
"""

import os
import shutil
import subprocess
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def main():
    base = Path.home() / "tmp/bbox-http-debug-validation"
    base.mkdir(parents=True, exist_ok=True)
    cache = base / "cache"
    cache.mkdir(exist_ok=True)
    scratch = base / "tmp"
    scratch.mkdir(exist_ok=True)
    env = dict(os.environ, GOMAXPROCS="2", GOMEMLIMIT="384MiB", GOGC="50",
               GOCACHE=str(cache), GOPATH=str(base / "go"), TMPDIR=str(scratch), GOTOOLCHAIN="local")
    # Match the x/net and x/text versions used by the pinned sing-box core.
    with tempfile.TemporaryDirectory(prefix="engine-", dir=base) as directory:
        project = Path(directory)
        for file in (ROOT / "libbox/httpdebug").glob("*.go"):
            shutil.copyfile(file, project / file.name)
        (project / "go.mod").write_text(
            "module github.com/sagernet/sing-box/experimental/httpdebug\n\ngo 1.25.5\n\n"
            "require (\n golang.org/x/net v0.57.0\n golang.org/x/text v0.40.0\n)\n"
        )
        subprocess.run(["go", "version"], env=env, check=True)
        subprocess.run(["go", "test", "-mod=mod", "-p=1", "-parallel=1", "-count=1", "-timeout=90s", "-v", "./..."],
                       cwd=project, env=env, check=True)


if __name__ == "__main__":
    main()
