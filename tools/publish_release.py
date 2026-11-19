"""Publish verified release assets without moving an existing version tag.

Invoked only by the release workflow when publish_release is enabled.
"""

import json
import os
import subprocess
from urllib.parse import quote

from verify_release import METADATA_NAME, read_release_metadata, read_version_properties, release_apks


def main():
    repository = os.environ['GITHUB_REPOSITORY']
    commit = os.environ['GITHUB_SHA']
    properties = read_version_properties()
    version = properties['VERSION_NAME']
    apks = release_apks(properties)
    metadata = apks[0].parent / METADATA_NAME
    # The verifier already checked APK identity and signature. Matching its
    # manifest rechecks those exact bytes before creating a tag or release.
    read_release_metadata(properties, apks, metadata)
    version_tag = f'v{version}'
    ref = f'refs/tags/{version_tag}'
    encoded_version = quote(version_tag, safe='')

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

    prerelease = '-' in version
    release_options = [
        '--repo', repository,
        '--title', f'B-box {version}',
        f'--prerelease={str(prerelease).lower()}',
        f'--latest={str(not prerelease).lower()}',
    ]
    existing = subprocess.run(
        ['gh', 'release', 'view', version_tag, '--repo', repository, '--json', 'tagName'],
        capture_output=True, text=True
    )
    if existing.returncode == 0:
        subprocess.run(
            ['gh', 'release', 'upload', version_tag, *(str(apk) for apk in apks), str(metadata),
             '--repo', repository, '--clobber'],
            check=True
        )
        subprocess.run(
            ['gh', 'release', 'edit', version_tag, '--draft=false', *release_options], check=True
        )
    else:
        subprocess.run(
            ['gh', 'release', 'create', version_tag, *(str(apk) for apk in apks), str(metadata),
             '--verify-tag', '--target', commit,
             '--notes', f'Signed ARM64 APK built from {commit}.', *release_options],
            check=True
        )
    print(f'Published {version_tag} from {commit}: {", ".join(apk.name for apk in apks)}')


if __name__ == "__main__":
    main()
