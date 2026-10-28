"""Source-level guardrails; runtime behavior is covered by Flutter/Android CI."""
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
frontend = ROOT / "flutter/lib"
native = ROOT / "app/src/main/java/io/nekohasekai/sfa"
glyph_source = (frontend / "ui/icons/app_glyphs.dart").read_text()
errors = []
for path in frontend.rglob("*.dart"):
    if "ui" in path.relative_to(frontend).parts:
        continue
    source = path.read_text()
    for name in set(re.findall(r"AppGlyphs\.(\w+)", source)):
        if not re.search(r"\b" + name + r"\b", glyph_source):
            errors.append(f"{path.relative_to(ROOT)}: unknown FlClash glyph {name}")
    for target in re.findall(r"import ['\"]([^'\"]+)", source):
        if ":" not in target and not (path.parent / target).is_file():
            errors.append(f"{path.relative_to(ROOT)}: missing import {target}")
for name in ("MainActivity", "ShareActivity"):
    source = (native / f"compose/{name}.kt").read_text()
    if f"class {name} : BoxActivity()" not in source or "setContent" in source:
        errors.append(f"{name} must enter the Flutter host")
for path in (ROOT / "app/src").rglob("*.kt"):
    source = path.read_text()
    if re.search(r"(?:androidx\.compose|androidx\.navigation|sfa\.compose\.(?:screen|navigation|component)|sfa\.update)\b", source):
        errors.append(f"{path.relative_to(ROOT)}: retired UI/update dependency")
for path in (ROOT / "app/build.gradle.kts", ROOT / "build.gradle.kts"):
    if re.search(r"plugin\.compose|androidx\.compose|navigation-(?:compose|fragment|ui)|sora-editor|libghostty-android-compose|app-update-ktx", path.read_text()):
        errors.append(f"{path.name}: retired UI/update build dependency")
for path in (ROOT / "app/src").rglob("AndroidManifest.xml"):
    if re.search(r"REQUEST_INSTALL_PACKAGES|UPDATE_PACKAGES_WITHOUT_USER_ACTION|InstallResultReceiver|WRITE_EXTERNAL_STORAGE", path.read_text()):
        errors.append(f"{path.relative_to(ROOT)}: retired installer/QR export permission")
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
if errors:
    raise SystemExit("\n".join(errors))
print("Flutter imports, glyphs and Android entry-point guards passed")
