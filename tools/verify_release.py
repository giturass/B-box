"""Verify the signed ARM64 release and generate its update metadata."""

import hashlib
import json
import os
import re
import subprocess
from pathlib import Path
from zipfile import ZipFile


APPLICATION_ID = 'io.ericlee.bfa'
RELEASE_DIRECTORY = Path('app/build/outputs/apk/other/release')
METADATA_NAME = 'B-box-version-metadata.json'


def read_version_properties(path=Path('version.properties')):
    properties = dict(
        line.strip().split('=', 1)
        for line in path.read_text().splitlines()
        if line.strip() and not line.lstrip().startswith('#') and '=' in line
    )
    if not re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?', properties.get('VERSION_NAME', '')):
        raise SystemExit('Invalid application VERSION_NAME in version.properties')
    if not re.fullmatch(r'[1-9][0-9]*', properties.get('VERSION_CODE', '')):
        raise SystemExit('Invalid application VERSION_CODE in version.properties')
    return properties


def release_apks(properties, directory=RELEASE_DIRECTORY):
    apks = sorted(directory.glob('*.apk'))
    expected_name = f"B-box-{properties['VERSION_NAME']}-arm64-v8a.apk"
    if len(apks) != 1 or apks[0].name != expected_name:
        raise SystemExit(f'Expected exactly one {expected_name}, found: {apks}')
    if not apks[0].is_file() or apks[0].is_symlink():
        raise SystemExit(f'Release APK must be a regular file: {apks[0]}')
    return apks


def artifact_manifest(apks):
    artifacts = []
    for apk in sorted(apks):
        digest = hashlib.sha256()
        size = 0
        with apk.open('rb') as source:
            for chunk in iter(lambda: source.read(1024 * 1024), b''):
                digest.update(chunk)
                size += len(chunk)
        if size == 0:
            raise SystemExit(f'Release APK is empty: {apk}')
        artifacts.append({'name': apk.name, 'sha256': digest.hexdigest(), 'size': size})
    return artifacts


def validate_metadata(metadata, properties, apks):
    """Recheck that metadata describes exactly the APK bytes being published."""
    if not isinstance(metadata, dict):
        raise SystemExit('Release metadata must be an object')
    expected_identity = {
        'application_id': APPLICATION_ID,
        'version_name': properties['VERSION_NAME'],
        'version_code': int(properties['VERSION_CODE']),
    }
    if type(metadata.get('version_code')) is not int:
        raise SystemExit('Release metadata version_code must be an integer')
    for key, expected in expected_identity.items():
        if metadata.get(key) != expected:
            raise SystemExit(f'Unexpected release metadata {key}: expected {expected!r}')
    artifacts = metadata.get('artifacts')
    if not isinstance(artifacts, list) or len(artifacts) != len(apks):
        raise SystemExit('Release metadata must describe every shipped APK exactly once')
    expected_names = {apk.name for apk in apks}
    names = set()
    for artifact in artifacts:
        if not isinstance(artifact, dict) or set(artifact) != {'name', 'sha256', 'size'}:
            raise SystemExit('Invalid release artifact metadata fields')
        name = artifact['name']
        if not isinstance(name, str) or name not in expected_names or name in names:
            raise SystemExit('Unexpected or duplicate release artifact name')
        names.add(name)
        if type(artifact['size']) is not int or artifact['size'] <= 0:
            raise SystemExit('Release artifact size must be a positive integer')
        if not isinstance(artifact['sha256'], str) or not re.fullmatch(r'[0-9a-f]{64}', artifact['sha256']):
            raise SystemExit('Release artifact sha256 must be a lowercase SHA-256 digest')
    if sorted(artifacts, key=lambda artifact: artifact['name']) != artifact_manifest(apks):
        raise SystemExit('Release APK size or SHA-256 differs from the verified metadata')
    return metadata


def read_release_metadata(properties, apks, path=None):
    path = path or (apks[0].parent / METADATA_NAME)
    try:
        metadata = json.loads(path.read_text())
    except (OSError, ValueError) as error:
        raise SystemExit(f'Cannot read verified release metadata: {error}') from error
    return validate_metadata(metadata, properties, apks)


def verify_apk(path, properties):
    with ZipFile(path) as apk:
        names = apk.namelist()
        abis = {name.split('/')[1] for name in names
                if name.startswith('lib/') and name.endswith('.so')}
        if any(name.endswith('/libbbox_capture.so') for name in names):
            raise SystemExit('Retired capture collector was packaged')
        if any(name.startswith('META-INF/xposed/') or name == 'assets/xposed_init' for name in names):
            raise SystemExit('Retired Xposed module metadata was packaged')
        for source in Path('app/src/main/assets/mitm-ca').rglob('*'):
            if not source.is_file():
                continue
            packaged = 'assets/' + source.relative_to('app/src/main/assets').as_posix()
            if packaged not in names or apk.read(packaged) != source.read_bytes():
                raise SystemExit(f'Missing or changed MITM module asset: {packaged}')
    if abis != {'arm64-v8a'}:
        raise SystemExit(f'Unexpected native ABIs: {abis}')

    aapt2 = Path(os.environ['ANDROID_HOME']) / 'build-tools/37.0.0/aapt2'
    badging = subprocess.check_output(
        [str(aapt2), 'dump', 'badging', str(path)], text=True
    )
    sdk_versions = re.findall(r"^(?:minSdkVersion|sdkVersion):'([^']*)'$", badging, re.MULTILINE)
    if sdk_versions != ['24']:
        raise SystemExit(f'Expected minimum Android SDK 24 (Android 7.0), found: {sdk_versions}')
    package_line = next(
        (line for line in badging.splitlines() if line.startswith('package: ')), ''
    )
    package = dict(re.findall(r"(\w+)='([^']*)'", package_line))
    expected_package = {
        'name': APPLICATION_ID,
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
    print(f'Verified ARM64 APK identity, minimum SDK 24 and {len(labels)} application labels: {path}')
    subprocess.run([str(aapt2.with_name('apksigner')), 'verify', '--verbose', str(path)], check=True)
    return {
        'application_id': package['name'],
        'version_code': int(package['versionCode']),
        'version_name': package['versionName'],
    }


def main():
    properties = read_version_properties()
    apks = release_apks(properties)
    identity = verify_apk(apks[0], properties)
    metadata = {**identity, 'artifacts': artifact_manifest(apks)}
    metadata_path = apks[0].parent / METADATA_NAME
    metadata_path.write_text(json.dumps(metadata, indent=2) + '\n')
    print(f'Generated verified update metadata: {metadata_path}')
    if os.environ.get('GITHUB_OUTPUT'):
        with Path(os.environ['GITHUB_OUTPUT']).open('a') as outputs:
            outputs.write(f"version={identity['version_name']}\n")


if __name__ == "__main__":
    main()
