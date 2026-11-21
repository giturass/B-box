"""Source-level guardrails; runtime behavior is covered by Flutter/Android CI."""
import re
import json
import hashlib
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
frontend = ROOT / "flutter/lib"
native = ROOT / "app/src/main/java/io/nekohasekai/sfa"
glyph_source = (frontend / "ui/icons/app_glyphs.dart").read_text()
errors = []
for path in frontend.rglob("*.dart"):
    source = path.read_text()
    if re.search(r"(?:import|export) ['\"]package:(?:material_ui/|cupertino_ui/|material_new_shapes/|super_sliver_list/|flutter/(?:material|cupertino)\.dart)", source):
        errors.append(f"{path.relative_to(ROOT)}: retired UI dependency; use prism_ui")
    if "ui" in path.relative_to(frontend).parts:
        continue
    for name in set(re.findall(r"AppGlyphs\.(\w+)", source)):
        if not re.search(r"\b" + name + r"\b", glyph_source):
            errors.append(f"{path.relative_to(ROOT)}: unknown FlClash glyph {name}")
    for target in re.findall(r"(?:import|export) ['\"]([^'\"]+)", source):
        if ":" not in target and not (path.parent / target).is_file():
            errors.append(f"{path.relative_to(ROOT)}: missing import {target}")
for name in ("MainActivity", "ShareActivity"):
    source = (native / f"compose/{name}.kt").read_text()
    if f"class {name} : BoxActivity()" not in source or "setContent" in source:
        errors.append(f"{name} must enter the Flutter host")
root_aidl = (ROOT / "app/src/main/aidl/io/nekohasekai/sfa/bg/IRootService.aidl").read_text()
if re.search(r"=\s*(?:2|3|10|11)\s*;", root_aidl):
    errors.append("Retired Root transactions 2, 3, 10 and 11 must not be reused")
for path in (ROOT / "app/src").rglob("*"):
    if path.suffix not in (".kt", ".java", ".aidl"):
        continue
    source = path.read_text()
    if re.search(r"(?:androidx\.compose|androidx\.navigation|sfa\.compose\.(?:screen|navigation|component)|sfa\.update)\b", source):
        errors.append(f"{path.relative_to(ROOT)}: retired UI/update dependency")
    if re.search(r"(?:sfa\.xposed|io\.github\.libxposed|de\.robv\.android\.xposed)\b|PrivilegeSettings|BoxPrivilege", source):
        errors.append(f"{path.relative_to(ROOT)}: retired privilege integration")
for path in (ROOT / "app/build.gradle.kts", ROOT / "build.gradle.kts", ROOT / "settings.gradle.kts"):
    if re.search(r"plugin\.compose|androidx\.compose|navigation-(?:compose|fragment|ui)|sora-editor|libghostty-android-compose|app-update-ktx", path.read_text()):
        errors.append(f"{path.name}: retired UI/update build dependency")
    if re.search(r"xposed", path.read_text(), re.I):
        errors.append(f"{path.name}: retired Xposed build dependency")
for path in (ROOT / "app/src").rglob("AndroidManifest.xml"):
    if re.search(r"UPDATE_PACKAGES_WITHOUT_USER_ACTION|InstallResultReceiver|WRITE_EXTERNAL_STORAGE", path.read_text()):
        errors.append(f"{path.relative_to(ROOT)}: retired installer/QR export permission")
    if re.search(r"xposed", path.read_text(), re.I):
        errors.append(f"{path.relative_to(ROOT)}: retired Xposed component or metadata")
for directory in (native / "compose", native / "update", native / "compat", ROOT / "app/src/github"):
    for path in directory.rglob("*"):
        if path.is_file() and path.name not in ("MainActivity.kt", "ShareActivity.kt"):
            errors.append(f"{path.relative_to(ROOT)}: retired source remains")
for path in (native / "flutter").glob("*.kt"):
    source = path.read_text()
    if re.search(r"(?:Screen|Dialog|ComposeView)\(", source):
        errors.append(f"{path.name}: legacy UI fallback is not allowed")
    if "NavController" in source or "navController.navigate" in source:
        errors.append(f"{path.name}: legacy navigation is not allowed")
for retired in ("flutter/lib/pages/capture_rules.dart", "core/sing-box/option/httpdebug.go", "tools/ebpf",
                "core/sing-box/experimental/httpdebug/wire.go", "flutter/lib/pages/tls_offsets.dart",
                "libbox", "tools/patch_httpdebug.py", "tools/patch_warp_masque.py",
                "tools/patch_xhttp_vless.py", "tools/patch_sing_tun.py", "tools/patch_processing_runtimes.py",
                "tools/verify_httpdebug.py",
                "app/src/main/java/io/nekohasekai/sfa/bg/RootCaptureSession.kt",
                "flutter/lib/pages/privilege.dart", "third_party/libxposed-api",
                "app/src/main/java/io/nekohasekai/sfa/xposed",
                "app/src/main/java/io/github/libxposed", "app/src/main/aidl/io/github/libxposed",
                "app/src/main/resources/META-INF/xposed", "app/src/main/assets/xposed_init"):
    path = ROOT / retired
    if path.is_file() or path.is_dir() and any(item.is_file() for item in path.rglob("*")):
        errors.append(f"{retired}: retired implementation remains")
manifest = json.loads((ROOT / "flutter/flclash-manifest.json").read_text())
flutter = ROOT / "flutter"


def check_commit(commit, label):
    if not re.fullmatch(r"[0-9a-f]{40}", commit):
        errors.append(f"{label} must pin a complete commit")


def check_hash(path, expected):
    if not re.fullmatch(r"[0-9a-f]{64}", expected):
        errors.append(f"Invalid source hash: {path.relative_to(ROOT)}")
    elif not path.is_file():
        errors.append(f"Missing imported source: {path.relative_to(ROOT)}")
    elif hashlib.sha256(path.read_bytes()).hexdigest() != expected:
        errors.append(f"Changed imported source: {path.relative_to(ROOT)}")


def check_adaptations(entries, local_root, source_files=None):
    for local, entry in entries.items():
        if not (local_root / local).is_file():
            errors.append(f"Missing adaptation: {local}")
        if "commit" in entry:
            check_commit(entry["commit"], local)
        sources = ([entry] if "source" in entry else []) + entry.get("sources", []) + entry.get("supportingSources", [])
        for source in sources:
            if not re.fullmatch(r"[0-9a-f]{64}", source["sourceSHA256"]):
                errors.append(f"Invalid adaptation source hash: {local}")
            if source_files is not None and source_files.get(source["source"]) != source["sourceSHA256"]:
                errors.append(f"Adaptation does not match pinned source: {local}")


check_commit(manifest["commit"], "FlClash")
for local, expected in manifest["files"].items():
    check_hash(flutter / local, expected)
for path in (frontend / "ui").rglob("*"):
    if (path.is_file()
            and path.relative_to(flutter).as_posix() not in manifest["files"]
            and path.relative_to(flutter).as_posix() not in manifest["adaptedFiles"]):
        errors.append(f"Untracked FlClash import: {path.relative_to(ROOT)}")
check_adaptations(manifest["adaptedFiles"], flutter)
check_adaptations(manifest["adaptedNativeFiles"], ROOT)

prism = manifest["prismUi"]
check_commit(prism["commit"], "prism_ui")
check_commit(prism["fork"]["commit"], "prism_ui fork")
for local, source in (("concord-LICENSE", "LICENSE"), ("concord-NOTICE", "THIRD_PARTY_NOTICES.md")):
    original = ROOT / "core/concord" / source
    if original.is_file():
        check_hash(flutter / "assets/licenses" / local, hashlib.sha256(original.read_bytes()).hexdigest())
    else:
        errors.append(f"Missing Concord license source: {source}")
upstreams = json.loads((ROOT / "upstreams.json").read_text())
for name, upstream in upstreams.items():
    check_commit(upstream["upstreamCommit"], name + " comparison baseline")
    path = ROOT / upstream["path"]
    if not (path / ".git").exists():
        errors.append(f"{name}: initialize the pinned Git submodule")
        continue
    url = subprocess.check_output(["git", "config", "-f", str(ROOT / ".gitmodules"), f"submodule.{name}.url"], text=True).strip()
    if url != upstream["fork"]:
        errors.append(f"{name}: submodule URL does not point to its fork")
    head = subprocess.check_output(["git", "-C", str(path), "rev-parse", "HEAD"], text=True).strip()
    entry = subprocess.check_output(["git", "-C", str(ROOT), "ls-files", "--stage", "--", upstream["path"]], text=True).split()
    if len(entry) < 2 or entry[0] != "160000" or entry[1] != head:
        errors.append(f"{name}: the staged Git submodule must pin the checked-out commit")
    if name == "prism_ui" and head != prism["fork"]["commit"]:
        errors.append("prism_ui fork commit does not match its source manifest")
tags = (ROOT / "core/sing-box/b-box/buildtags/android.txt").read_text().strip().split(",")
required_tags = {"with_quic", "with_wireguard", "with_utls", "with_naive_outbound", "with_clash_api", "with_xhttp",
                 "with_easytier", "with_ebpf", "with_concord", "with_usbip", "with_acme", "with_dhcp", "with_openvpn",
                 "with_openconnect", "with_cloudflared", "with_ccm", "with_ocm", "with_v2ray_api", "with_tailscale"}
if not required_tags.issubset(tags) or any(tag.startswith("ts_omit_") for tag in tags):
    errors.append("The shipped Android core must retain all MiChongs features and full Tailscale")
check_commit(prism["flclashSubmoduleCommit"], "FlClash prism_ui submodule reference")
prism_root = flutter / prism["localPath"]
for local, expected in prism["files"].items():
    check_hash(prism_root / local, expected)
for directory in (prism_root / "lib", prism_root / "shaders", prism_root / "test"):
    for path in directory.rglob("*"):
        if path.is_file() and path.relative_to(prism_root).as_posix() not in prism["files"]:
            errors.append(f"Untracked prism_ui import: {path.relative_to(ROOT)}")
prism_sources = dict(prism["files"])
for local, entry in prism["adaptedFiles"].items():
    if local.startswith(prism["localPath"] + "/"):
        prism_sources[entry["source"]] = entry["sourceSHA256"]
check_adaptations(prism["adaptedFiles"], flutter, prism_sources)
for local, entry in prism["licenseAssets"].items():
    check_hash(flutter / local, entry["sha256"])
    original = prism_root / entry["source"]
    check_hash(original, entry["sourceSHA256"])
    if not original.is_file() or not (flutter / local).is_file():
        continue
    original_text = original.read_text()
    if entry["usage"] == "header":
        header = re.match(r"/\*(.*?)\*/", original_text, re.S)
        original_text = (header[1].strip() if header else "") + "\n"
    if (flutter / local).read_text() != original_text:
        errors.append(f"License does not match prism_ui source: {local}")
pubspec = (flutter / "pubspec.yaml").read_text()
if not re.search(r"(?m)^  prism_ui:\n    path: " + re.escape(prism["localPath"]) + r"\s*$", pubspec):
    errors.append("Flutter must use the pinned local prism_ui package")
if not re.search(r"(?m)^version: " + re.escape(prism["version"]) + r"\s*$", (prism_root / "pubspec.yaml").read_text()):
    errors.append("prism_ui package version does not match its source manifest")
for reference in manifest.get("supplementalReferences", []):
    if not re.fullmatch(r"[0-9a-f]{40}", reference["commit"]):
        errors.append("Supplemental FlClash reference must pin a complete commit")
    for source in reference["files"]:
        if not re.fullmatch(r"[0-9a-f]{64}", source["sourceSHA256"]):
            errors.append(f"Invalid supplemental source hash: {source['source']}")
        for local in source["localPaths"]:
            path = ROOT / local
            if not path.is_file():
                errors.append(f"Missing supplemental reference target: {local}")
            elif source["usage"] == "verbatim" and hashlib.sha256(path.read_bytes()).hexdigest() != source["sourceSHA256"]:
                errors.append(f"Changed verbatim supplemental import: {local}")
if errors:
    raise SystemExit("\n".join(errors))
print("Fork gitlinks, complete core tags, FlClash/Prism/Concord sources and licenses, Android entries and retired feature guards passed")
