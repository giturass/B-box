"""Run the real libbox/Room/service recovery regressions in the smoke emulator."""
import re
import subprocess
from pathlib import Path

out = Path("build/android-smoke")
out.mkdir(parents=True, exist_ok=True)
apks = list(Path("build/apk").rglob("*-androidTest.apk"))
assert len(apks) == 1, apks
subprocess.run(["adb", "install", "-r", str(apks[0])], check=True, timeout=120)
result = subprocess.run([
    "adb", "shell", "am", "instrument", "-w", "-r",
    "io.ericlee.bfa.test/androidx.test.runner.AndroidJUnitRunner",
], capture_output=True, text=True, timeout=360)
(out / "native-tests.txt").write_text(result.stdout + result.stderr)
print(result.stdout)
assert result.returncode == 0 and re.search(r"\bOK \([1-9]\d* tests?\)", result.stdout), result.stdout + result.stderr
