"""Source-level guardrails; runtime behavior is covered by Flutter/Android CI."""
import re
import json
import hashlib
import subprocess
import xml.etree.ElementTree as ET
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
frontend = ROOT / "flutter/lib"
native = ROOT / "app/src/main/java/io/nekohasekai/sfa"
glyph_source = (frontend / "ui/icons/app_glyphs.dart").read_text()
errors = []
surge_module_feature = re.compile(
    r"\b(?:SurgeModulesPage|SurgePanelsPage|SurgeModuleActions|BoxPanelCache|ModuleGet|"
    r"Describe(?:Profile)?Surge(?:Module|Panels)|NewUIModulePanelTask|RunPanel|ModuleOptions)\b"
    r"|profiles\.module(?:File|Fetch|Describe|Details|Panels)\b", re.I)
for path in frontend.rglob("*.dart"):
    source = path.read_text()
    if surge_module_feature.search(source):
        errors.append(f"{path.relative_to(ROOT)}: retired Surge module integration")
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
android = "{http://schemas.android.com/apk/res/android}"
application = ET.parse(ROOT / "app/src/main/AndroidManifest.xml").getroot().find("application")
if application is None:
    errors.append("Android application manifest is missing")
else:
    if application.get(android + "process"):
        errors.append("Native application state must remain in the default process")
    activities = {node.get(android + "name"): node for node in application.findall("activity")}
    for name in ("MainActivity", "ShareActivity"):
        node = activities.get(f".compose.{name}")
        if node is None or node.get(android + "process") != ":ui":
            errors.append(f"{name} must run in the separate :ui process")
    services = {node.get(android + "name"): node for node in application.findall("service")}
    ipc_services = (".flutter.ipc.BoxUiService", ".terminal.BoxTerminalService")
    for name in (*ipc_services, ".bg.VPNService", ".bg.ProxyService", ".bg.USBIPService", ".bg.TaildropSendService"):
        node = services.get(name)
        if node is None or node.get(android + "process"):
            errors.append(f"{name} must remain in the default process")
        if name in ipc_services and (node is None or node.get(android + "exported") != "false"):
            errors.append(f"{name} must be private to this application")
    idle_service = services.get(".flutter.UiIdleJobService")
    if idle_service is None or idle_service.get(android + "process") != ":ui":
        errors.append("UI idle jobs must run in the separate :ui process")
    if (idle_service is None or idle_service.get(android + "permission") != "android.permission.BIND_JOB_SERVICE"
            or idle_service.get(android + "exported") != "true"):
        errors.append("UI idle jobs must be accessible only to Android's protected JobScheduler binding")
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
    if surge_module_feature.search(source):
        errors.append(f"{path.relative_to(ROOT)}: retired Surge module integration")
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
for retired in ("flutter/lib/pages/capture_rules.dart", "core/option/httpdebug.go", "tools/ebpf",
                "core/experimental/httpdebug/wire.go", "flutter/lib/pages/tls_offsets.dart",
                "flutter/lib/pages/surge_modules.dart", "flutter/lib/pages/surge_panels.dart",
                "flutter/test/surge_module_test.dart", "tools/surge_catalog",
                "core/experimental/libbox/ui_modules.go", "core/experimental/libbox/ui_modules_test.go",
                "core/experimental/libbox/ui_module_rules.go", "core/experimental/libbox/ui_module_panels.go",
                "app/src/main/java/io/nekohasekai/sfa/flutter/BoxPanelCache.kt",
                "app/src/androidTest/java/io/nekohasekai/sfa/flutter/BoxPanelCacheTest.kt",
                "app/src/main/java/io/nekohasekai/sfa/bg/RootCaptureSession.kt",
                "flutter/lib/pages/privilege.dart", "third_party/libxposed-api",
                "app/src/main/java/io/nekohasekai/sfa/xposed",
                "app/src/main/java/io/github/libxposed", "app/src/main/aidl/io/github/libxposed",
                "app/src/main/resources/META-INF/xposed", "app/src/main/assets/xposed_init"):
    path = ROOT / retired
    if path.is_file() or path.is_dir() and any(item.is_file() for item in path.rglob("*")):
        errors.append(f"{retired}: retired implementation remains")
for directory in ("core/experimental/libbox", "core/experimental/httpdebug", "core/option"):
    for path in (ROOT / directory).glob("*.go"):
        if surge_module_feature.search(path.read_text()):
            errors.append(f"{path.relative_to(ROOT)}: retired Surge module integration")
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
check_commit(prism["flclashSubmoduleCommit"], "FlClash prism_ui submodule reference")
prism_root = flutter / prism["localPath"]
check_commit(prism["forkCommit"], "prism_ui fork")
modules = subprocess.run(["git", "config", "--file", ".gitmodules", "--get-regexp", r"^submodule\..*\.(path|url|branch)$"],
                         cwd=ROOT, capture_output=True, text=True).stdout.split("\n")
for path, url in (("flutter/" + prism["localPath"], prism["forkRepository"]), ("core", "https://github.com/giturass/sing-box")):
    for key, value in (("path", path), ("url", url), ("branch", "b-box")):
        if f"submodule.{path}.{key} {value}" not in modules:
            errors.append(f"{path} must be the {url} b-box submodule")
    link = subprocess.run(["git", "ls-files", "--stage", "--", path], cwd=ROOT, capture_output=True, text=True).stdout.split()
    if link[:1] != ["160000"]:
        errors.append(f"{path} must be tracked as a submodule")
    elif path != "core" and link[1] != prism["forkCommit"]:
        errors.append("prism_ui submodule does not match forkCommit in the source manifest")
if not (prism_root / "pubspec.yaml").is_file():
    raise SystemExit("\n".join(errors + ["prism_ui submodule is not checked out; run git submodule update --init flutter/packages/prism_ui"]))
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
print("FlClash/prism_ui sources, licenses, Flutter imports, glyphs, Android entry points and retired feature guards passed")
