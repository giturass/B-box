"""Install B-box's Cloudflare WARP MASQUE transport into the matching core."""

import argparse
import shutil
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("core", type=Path, help="clean matching sing-box checkout")
    core = parser.parse_args().core.resolve()
    source = ROOT / "libbox/masque"
    files = {
        "option.go": "option/b_box_masque.go",
        "outbound.go": "protocol/masque/b_box_outbound.go",
        **{
            name: f"transport/masque/b_box_{name}"
            for name in (
                "warp.go", "warp_http2.go", "warp_http3.go", "warp_http3_stub.go",
                "warp_test.go", "warp_http3_test.go",
            )
        },
    }
    if any((core / target).exists() for target in files.values()):
        raise SystemExit("WARP MASQUE overlay already exists; use a clean matching core checkout")
    for name, target in files.items():
        if not (source / name).is_file() or not (core / target).parent.is_dir():
            raise SystemExit(f"Missing WARP MASQUE source or core directory for {name}")
    patch = ROOT / "libbox/warp-masque.patch"
    subprocess.run(["git", "apply", "--check", str(patch)], cwd=core, check=True)
    subprocess.run(["git", "apply", str(patch)], cwd=core, check=True)
    for name, target in files.items():
        shutil.copyfile(source / name, core / target)
    print("Installed WARP MASQUE outbound, HTTP/2 and HTTP/3 transports and regression tests")


if __name__ == "__main__":
    main()
