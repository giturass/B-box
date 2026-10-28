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
for path in (native / "flutter").glob("*.kt"):
    source = path.read_text()
    if re.search(r"(?:Screen|Dialog|ComposeView)\(", source):
        errors.append(f"{path.name}: legacy UI fallback is not allowed")
    if "NavController" in source or "navController.navigate" in source:
        errors.append(f"{path.name}: legacy navigation is not allowed")
if errors:
    raise SystemExit("\n".join(errors))
print("Flutter imports, glyphs and Android entry-point guards passed")
