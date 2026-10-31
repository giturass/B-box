"""Publish verified release assets without moving an existing version tag.

Invoked only by the release workflow when publish_release is enabled.
"""

import json
import os
import re
import subprocess
from pathlib import Path
from urllib.parse import quote


def main():
    repository = os.environ['GITHUB_REPOSITORY']
    commit = os.environ['GITHUB_SHA']
    version = os.environ['VERSION_NAME']
    properties = dict(
        line.strip().split('=', 1)
        for line in Path('version.properties').read_text().splitlines()
        if line.strip() and not line.lstrip().startswith('#') and '=' in line
    )
    if version != properties['VERSION_NAME']:
        raise SystemExit('Release tag must exactly match VERSION_NAME in version.properties')
    ref = f'refs/tags/{version}'
    encoded_version = quote(version, safe='')

    def api(path, *arguments):
        return json.loads(subprocess.check_output(
            ['gh', 'api', f'repos/{repository}/{path}', *arguments], text=True
        ))

    def find_tag():
        refs = api(f'git/matching-refs/tags/{encoded_version}')
        return next((entry for entry in refs if entry['ref'] == ref), None)

    tag = find_tag()
    if tag is None:
        try:
            api('git/refs', '--method', 'POST', '-f', f'ref={ref}', '-f', f'sha={commit}')
        except subprocess.CalledProcessError:
            # Another run may have created this tag in the meantime.
            if find_tag() is None:
                raise
        tag = find_tag()
    if tag is None:
        raise SystemExit(f'Cannot read release tag after creation: {ref}')
    target = tag['object']
    while target['type'] == 'tag':
        target = api(f"git/tags/{target['sha']}")['object']
    if target['type'] != 'commit' or target['sha'] != commit:
        raise SystemExit(
            f'Refusing to publish: {ref} points to {target["sha"]}, expected {commit}. '
            'Existing release tags are never moved.'
        )

    apk = Path('app/build/outputs/apk/other/release') / f'B-box-{version}-arm64-v8a.apk'
    metadata = apk.parent / 'B-box-version-metadata.json'
    metadata.write_text(json.dumps({
        'version_code': int(properties['VERSION_CODE']),
        'version_name': version,
        'application_id': 'io.ericlee.bfa',
    }, indent=2) + '\n')
    prerelease = bool(re.search(r'(alpha|beta|rc)', version, re.IGNORECASE))
    release_options = [
        '--repo', repository,
        '--title', f'B-box {version}',
        f'--prerelease={str(prerelease).lower()}',
        f'--latest={str(not prerelease).lower()}',
    ]
    existing = subprocess.run(
        ['gh', 'release', 'view', version, '--repo', repository, '--json', 'tagName'],
        capture_output=True, text=True
    )
    if existing.returncode == 0:
        subprocess.run(
            ['gh', 'release', 'upload', version, str(apk), str(metadata),
             '--repo', repository, '--clobber'],
            check=True
        )
        subprocess.run(
            ['gh', 'release', 'edit', version, '--draft=false', *release_options], check=True
        )
    else:
        subprocess.run(
            ['gh', 'release', 'create', version, str(apk), str(metadata),
             '--verify-tag', '--target', commit,
             '--notes', f'Signed ARM64 APK built from {commit}.', *release_options],
            check=True
        )
    print(f'Published {version} from {commit}: {apk.name}')


if __name__ == "__main__":
    main()
