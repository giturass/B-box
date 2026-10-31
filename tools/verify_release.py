"""Verify the signed ARM64 release artifact before upload or publication."""

import os
import re
import subprocess
from pathlib import Path
from zipfile import ZipFile


def main():
    properties = dict(
        line.strip().split('=', 1)
        for line in Path('version.properties').read_text().splitlines()
        if line.strip() and not line.lstrip().startswith('#') and '=' in line
    )
    apks = list(Path('app/build/outputs/apk/other/release').glob('*.apk'))
    expected_name = f"B-box-{properties['VERSION_NAME']}-arm64-v8a.apk"
    if len(apks) != 1 or apks[0].name != expected_name:
        raise SystemExit(f'Expected exactly one {expected_name}, found: {apks}')
    with ZipFile(apks[0]) as apk:
        abis = {name.split('/')[1] for name in apk.namelist()
                if name.startswith('lib/') and name.endswith('.so')}
    if abis != {'arm64-v8a'}:
        raise SystemExit(f'Unexpected native ABIs: {abis}')

    aapt2 = Path(os.environ['ANDROID_HOME']) / 'build-tools/37.0.0/aapt2'
    badging = subprocess.check_output(
        [str(aapt2), 'dump', 'badging', str(apks[0])], text=True
    )
    sdk_versions = re.findall(r"^(?:minSdkVersion|sdkVersion):'([^']*)'$", badging, re.MULTILINE)
    if sdk_versions != ['24']:
        raise SystemExit(f'Expected minimum Android SDK 24 (Android 7.0), found: {sdk_versions}')
    package_line = next(
        (line for line in badging.splitlines() if line.startswith('package: ')), ''
    )
    package = dict(re.findall(r"(\w+)='([^']*)'", package_line))
    expected_package = {
        'name': 'io.ericlee.bfa',
        'versionName': properties['VERSION_NAME'],
        'versionCode': properties['VERSION_CODE'],
    }
    for key, expected in expected_package.items():
        if package.get(key) != expected:
            raise SystemExit(f'Unexpected APK {key}: {package.get(key)!r}, expected {expected!r}')
    launcher_line = next(
        (line for line in badging.splitlines() if line.startswith('launchable-activity: ')), ''
    )
    launcher = dict(re.findall(r"(\w+)='([^']*)'", launcher_line))
    if launcher.get('name') != 'io.nekohasekai.sfa.compose.MainActivity':
        raise SystemExit(f'Unexpected launcher activity: {launcher.get("name")!r}')
    labels = dict(re.findall(r"^(application-label(?:-[^:]+)?):'(.*)'$", badging, re.MULTILINE))
    if 'application-label' not in labels or any(label != 'B-box' for label in labels.values()):
        raise SystemExit(f'Unexpected application labels: {labels}')
    print(f'Verified ARM64 APK identity, minimum SDK 24 and {len(labels)} application labels: {apks[0]}')
    subprocess.run([str(aapt2.with_name('apksigner')), 'verify', '--verbose', str(apks[0])], check=True)


if __name__ == "__main__":
    main()
