"""Release/version regressions run by GitHub Actions without building an APK."""

import copy
import hashlib
import json
import os
import re
import subprocess
import tempfile
import textwrap
import unittest
from pathlib import Path
from unittest.mock import patch
from zipfile import ZipFile

import publish_release
import verify_release


REPOSITORY = Path(__file__).resolve().parents[1]
SOURCE_SHA = 'a' * 40
OTHER_SHA = 'b' * 40
PROPERTIES = (
    'VERSION_CODE=749\n'
    'VERSION_NAME=1.0.1\n'
    'CORE_VERSION_NAME=1.15.0-alpha.13\n'
    'GO_VERSION=go1.26.10\n'
)
BADGING = (
    "package: name='io.ericlee.bfa' versionCode='749' versionName='1.0.1'\n"
    "sdkVersion:'24'\n"
    "application-label:'B-box'\n"
    "launchable-activity: name='io.nekohasekai.sfa.compose.MainActivity'\n"
)


class ReleaseTests(unittest.TestCase):
    def setUp(self):
        temporary_root = Path.home() / 'tmp'
        temporary_root.mkdir(parents=True, exist_ok=True)
        self.temporary = tempfile.TemporaryDirectory(prefix='bbox-release-', dir=temporary_root)
        self.previous_directory = Path.cwd()
        os.chdir(self.temporary.name)
        Path('version.properties').write_text(PROPERTIES)
        self.directory = verify_release.RELEASE_DIRECTORY
        self.directory.mkdir(parents=True)
        self.apk = self.directory / 'B-box-1.0.1-arm64-v8a.apk'
        self.apk.write_bytes(b'signed APK fixture')
        self.metadata_path = self.directory / verify_release.METADATA_NAME
        self.environment = patch.dict(os.environ, {
            'GITHUB_REPOSITORY': 'giturass/B-box',
            'GITHUB_SHA': SOURCE_SHA,
            'GITHUB_OUTPUT': str(Path(self.temporary.name) / 'github-output'),
            'ANDROID_HOME': str(Path(self.temporary.name) / 'android-sdk'),
        })
        self.environment.start()

    def tearDown(self):
        self.environment.stop()
        os.chdir(self.previous_directory)
        self.temporary.cleanup()

    def metadata(self, apks=None):
        return {
            'application_id': 'io.ericlee.bfa',
            'version_code': 749,
            'version_name': '1.0.1',
            'artifacts': [
                {'name': apk.name, 'sha256': hashlib.sha256(apk.read_bytes()).hexdigest(),
                 'size': len(apk.read_bytes())}
                for apk in (apks or [self.apk])
            ],
        }

    def write_metadata(self, metadata=None):
        self.metadata_path.write_text(json.dumps(metadata or self.metadata()))

    def version_script(self):
        lines = (REPOSITORY / '.github/actions/setup-core/action.yml').read_text().splitlines()
        first_run = next(index for index, line in enumerate(lines) if line.strip() == 'run: |')
        body = []
        for line in lines[first_run + 1:]:
            if line and not line.startswith('        '):
                break
            body.append(line)
        return textwrap.dedent('\n'.join(body))

    def run_version_script(self):
        return subprocess.run(
            ['bash', '--noprofile', '--norc', '-e', '-o', 'pipefail', '-c', self.version_script()],
            capture_output=True, text=True,
        )

    def test_core_checkout_version_stays_independent_of_app_version(self):
        result = self.run_version_script()
        self.assertEqual(result.returncode, 0, result.stderr)
        output = Path(os.environ['GITHUB_OUTPUT']).read_text()
        self.assertEqual(output, 'version=1.15.0-alpha.13\ngo_version=1.26.10\n')
        Path('version.properties').write_text(PROPERTIES.replace('VERSION_NAME=1.0.1', 'VERSION_NAME=9.8.7'))
        Path(os.environ['GITHUB_OUTPUT']).unlink()
        result = self.run_version_script()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(Path(os.environ['GITHUB_OUTPUT']).read_text(), output)

    def test_core_version_is_required_without_falling_back_to_app_version(self):
        Path('version.properties').write_text(PROPERTIES.replace('CORE_VERSION_NAME=1.15.0-alpha.13\n', ''))
        self.assertNotEqual(self.run_version_script().returncode, 0)
        self.assertFalse(Path(os.environ['GITHUB_OUTPUT']).exists())

    def test_flutter_package_version_matches_application_version(self):
        properties = verify_release.read_version_properties(REPOSITORY / 'version.properties')
        flutter_version = re.search(
            r'^version: (.+)$', (REPOSITORY / 'flutter/pubspec.yaml').read_text(), re.MULTILINE
        ).group(1)
        self.assertEqual(flutter_version, f"{properties['VERSION_NAME']}+{properties['VERSION_CODE']}")
        self.assertGreater(int(properties['VERSION_CODE']), 748)

    def test_application_version_rejects_names_the_update_parser_cannot_match(self):
        for version in ['1.0', 'v1.0.1', '1.0.1+build', '1.0.1/other', '1.0.1-']:
            with self.subTest(version=version):
                Path('version.properties').write_text(PROPERTIES.replace('VERSION_NAME=1.0.1', f'VERSION_NAME={version}'))
                with self.assertRaises(SystemExit):
                    verify_release.read_version_properties()

    def test_verified_apk_produces_metadata_without_publishing(self):
        with ZipFile(self.apk, 'w') as apk:
            apk.writestr('lib/arm64-v8a/libbox.so', b'native library fixture')
        with patch('verify_release.subprocess.check_output', return_value=BADGING), \
                patch('verify_release.subprocess.run') as signing:
            verify_release.main()
        signing.assert_called_once()
        self.assertEqual(signing.call_args.args[0][1:3], ['verify', '--verbose'])
        self.assertEqual(json.loads(self.metadata_path.read_text()), self.metadata())
        self.assertEqual(Path(os.environ['GITHUB_OUTPUT']).read_text(), 'version=1.0.1\n')

    def test_failed_identity_or_signature_does_not_generate_metadata(self):
        with ZipFile(self.apk, 'w') as apk:
            apk.writestr('lib/arm64-v8a/libbox.so', b'native library fixture')
        with patch('verify_release.subprocess.check_output', return_value=BADGING.replace("'749'", "'748'")), \
                patch('verify_release.subprocess.run') as signing:
            with self.assertRaises(SystemExit):
                verify_release.main()
            signing.assert_not_called()
        with patch('verify_release.subprocess.check_output', return_value=BADGING), \
                patch('verify_release.subprocess.run', side_effect=subprocess.CalledProcessError(1, 'apksigner')):
            with self.assertRaises(subprocess.CalledProcessError):
                verify_release.main()
        self.assertFalse(self.metadata_path.exists())
        self.assertFalse(Path(os.environ['GITHUB_OUTPUT']).exists())

    def test_metadata_tracks_all_artifact_bytes_and_accepts_either_order(self):
        self.apk.write_bytes(b'first' * (1024 * 1024))
        second = self.directory / 'B-box-1.0.1-x86_64.apk'
        second.write_bytes(b'second APK fixture')
        expected = self.metadata([self.apk, second])
        self.assertEqual(verify_release.artifact_manifest([second, self.apk]), expected['artifacts'])
        expected['artifacts'].reverse()
        self.assertEqual(verify_release.validate_metadata(
            expected, verify_release.read_version_properties(), [self.apk, second]
        ), expected)

    def test_metadata_rejects_duplicate_or_missing_artifacts(self):
        second = self.directory / 'B-box-1.0.1-x86_64.apk'
        second.write_bytes(b'second APK fixture')
        properties = verify_release.read_version_properties()
        metadata = self.metadata([self.apk, second])
        metadata['artifacts'][1] = metadata['artifacts'][0].copy()
        with self.assertRaises(SystemExit):
            verify_release.validate_metadata(metadata, properties, [self.apk, second])
        with self.assertRaises(SystemExit):
            verify_release.validate_metadata(self.metadata(), properties, [self.apk, second])

    def test_publish_uses_v_app_tag_and_stable_status_with_alpha_core(self):
        self.write_metadata()
        tag = []

        def api(command, **arguments):
            if command[2].endswith('git/matching-refs/tags/v1.0.1'):
                return json.dumps(tag)
            self.assertTrue(command[2].endswith('git/refs'))
            self.assertIn('ref=refs/tags/v1.0.1', command)
            self.assertIn(f'sha={SOURCE_SHA}', command)
            tag.append({'ref': 'refs/tags/v1.0.1', 'object': {'type': 'commit', 'sha': SOURCE_SHA}})
            return json.dumps(tag[0])

        with patch('publish_release.subprocess.check_output', side_effect=api), \
                patch('publish_release.subprocess.run', return_value=subprocess.CompletedProcess([], 1)) as release:
            publish_release.main()
        commands = [call.args[0] for call in release.call_args_list]
        creation = next(command for command in commands if command[1:3] == ['release', 'create'])
        self.assertEqual(creation[3], 'v1.0.1')
        self.assertIn(str(self.apk), creation)
        self.assertIn(str(self.metadata_path), creation)
        self.assertIn('--prerelease=false', creation)
        self.assertIn('--latest=true', creation)
        self.assertNotIn('1.15.0-alpha.13', ' '.join(creation))

    def test_same_commit_existing_tag_allows_asset_reupload(self):
        self.write_metadata()
        refs = [{'ref': 'refs/tags/v1.0.1', 'object': {'type': 'commit', 'sha': SOURCE_SHA}}]
        with patch('publish_release.subprocess.check_output', return_value=json.dumps(refs)) as api, \
                patch('publish_release.subprocess.run', return_value=subprocess.CompletedProcess([], 0)) as release:
            publish_release.main()
        self.assertEqual(api.call_count, 1)
        commands = [call.args[0] for call in release.call_args_list]
        upload = next(command for command in commands if command[1:3] == ['release', 'upload'])
        self.assertEqual(upload[3], 'v1.0.1')
        self.assertIn('--clobber', upload)
        self.assertFalse(any(command[1:3] == ['release', 'create'] for command in commands))

    def test_any_app_prerelease_suffix_controls_release_status(self):
        version = '1.0.1-preview.1'
        Path('version.properties').write_text(PROPERTIES.replace('VERSION_NAME=1.0.1', f'VERSION_NAME={version}'))
        self.apk = self.apk.rename(self.directory / f'B-box-{version}-arm64-v8a.apk')
        metadata = self.metadata()
        metadata['version_name'] = version
        self.write_metadata(metadata)
        refs = [{'ref': f'refs/tags/v{version}', 'object': {'type': 'commit', 'sha': SOURCE_SHA}}]
        with patch('publish_release.subprocess.check_output', return_value=json.dumps(refs)), \
                patch('publish_release.subprocess.run', return_value=subprocess.CompletedProcess([], 1)) as release:
            publish_release.main()
        creation = next(call.args[0] for call in release.call_args_list if call.args[0][1:3] == ['release', 'create'])
        self.assertEqual(creation[3], f'v{version}')
        self.assertIn('--prerelease=true', creation)
        self.assertIn('--latest=false', creation)

    def test_existing_tag_at_another_commit_is_never_moved(self):
        self.write_metadata()
        refs = [{'ref': 'refs/tags/v1.0.1', 'object': {'type': 'commit', 'sha': OTHER_SHA}}]
        with patch('publish_release.subprocess.check_output', return_value=json.dumps(refs)) as api, \
                patch('publish_release.subprocess.run') as release:
            with self.assertRaisesRegex(SystemExit, 'Existing release tags are never moved'):
                publish_release.main()
        self.assertEqual(api.call_count, 1)
        release.assert_not_called()

    def test_invalid_metadata_fails_before_any_tag_or_release_request(self):
        valid = self.metadata()
        invalid = []
        for key, value in [('application_id', 'io.nekohasekai.sfa'), ('version_code', 748),
                           ('version_name', '1.15.0-alpha.13')]:
            metadata = copy.deepcopy(valid)
            metadata[key] = value
            invalid.append(metadata)
        for key, value in [('sha256', '0' * 64), ('size', valid['artifacts'][0]['size'] + 1),
                           ('size', True), ('name', '../B-box-1.0.1-arm64-v8a.apk')]:
            metadata = copy.deepcopy(valid)
            metadata['artifacts'][0][key] = value
            invalid.append(metadata)
        invalid.append({key: value for key, value in valid.items() if key != 'artifacts'})
        for metadata in invalid:
            with self.subTest(metadata=metadata):
                self.write_metadata(metadata)
                with patch('publish_release.subprocess.check_output') as api, \
                        patch('publish_release.subprocess.run') as release:
                    with self.assertRaises(SystemExit):
                        publish_release.main()
                api.assert_not_called()
                release.assert_not_called()

    def test_changed_apk_is_rejected_even_when_size_is_unchanged(self):
        self.write_metadata()
        original = self.apk.read_bytes()
        self.apk.write_bytes(b'X' + original[1:])
        with patch('publish_release.subprocess.check_output') as api, \
                patch('publish_release.subprocess.run') as release:
            with self.assertRaisesRegex(SystemExit, 'size or SHA-256'):
                publish_release.main()
        api.assert_not_called()
        release.assert_not_called()

    def test_core_named_apk_cannot_be_published_as_app_release(self):
        self.apk.rename(self.directory / 'B-box-1.15.0-alpha.13-arm64-v8a.apk')
        with self.assertRaisesRegex(SystemExit, 'B-box-1.0.1-arm64-v8a.apk'):
            verify_release.release_apks(verify_release.read_version_properties())


if __name__ == '__main__':
    unittest.main()
