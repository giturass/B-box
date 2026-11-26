import argparse
import hashlib
import ipaddress
import json
import os
from pathlib import Path
import subprocess
import ssl
import tarfile
import tempfile
from concurrent.futures import ThreadPoolExecutor


ROOT = Path(__file__).resolve().parents[2]
SNAPSHOT = '04e93ac904d9f7a3005eea3b6f90e29b29073b66'
MANIFEST = '9ad7285ae5799209353c19e7dee11aade5d4107de4c0819ff805d8713b902e83'
AUTHOR = 'https://raw.githubusercontent.com/deezertidal/Surge_Module/7c1bb21e3cb86c04278c43bc22947899753c7cf4/files'
SCRIPTS = {
    'rates': '6f438d89e01fe99e0142c03a72e240067382b9800c55a037fb6f5f5585a64477',
    'crypto': '1d28bf7392768c76c086b9a8f0ffcd49fd06038052e027739fbd73f4d1f113e0',
    'histoday': '803ba7f250ddd7450cbffb408467a954dbd830c6fbb694047a3b9b660f1825c0',
    'hotoday': 'defcab194c371a08fa4a5482f001f53731a5a5ed366d6b6a12cd187f1c734a23',
    'book': '20a6d4d9fb7947144a7c0f6708258192fb218bd6c62cb98d753a00b61a5e189c',
    'car': '7639c53a167af60c7c1c6024214b4993957d8a09f08c038d090181af32ada1fd',
    'rumors': 'a01e42178f383a25cedb4a0a8886eb8e3b81f9953e99add044a2485d70b95b0a',
    'movie': 'a41ccfa32c961cc3d48938dc7aa4b8ce6723083be3220809f492c22660975996',
    'tianqi': '488cef6152c9604c8efd9cbafa965494851a126806557731116660e7ee2ecc71',
    'tv': '581e273e4a7a43678401fc8bbd60409f13db8d72b3e8c3b8e7c6de469e52e53e',
    'nm': '973b366e36e82f12bc22f1e49f53aab7206f84fc062d27e681c29e4790d572d0',
    'qm': 'a41905f6df54f835bc2d99818750654093c01f9d5ac7dc3c56355293c239ab93',
    'bb100': 'd654b7a1b09aa4fa148e97555c5350a8313da3f0ca6e3580783d11b346b0fde2',
}


def download(url, target):
    subprocess.run(['curl', '--proto', '=https', '--fail', '--silent', '--show-error', '--retry', '2',
                    '--max-time', '60', '--max-filesize', '16777216', '--output', target, url], check=True)


def checked(data, digest):
    if hashlib.sha256(data).hexdigest() != digest:
        raise ValueError(f'Content does not match SHA-256 {digest}')
    return data


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--live-rates', action='store_true', help='Also run the pinned rates panel against the public CNY API over verified TLS')
    parser.add_argument('--report', type=Path, help='Write per-module parser results and the names of passed behavioral tests to JSON')
    parser.add_argument('--verbose', action='store_true', help='Print all individual requests and service logs')
    args = parser.parse_args()
    core = ROOT / 'core'
    with tempfile.TemporaryDirectory(prefix='b-box-surge-') as temporary:
        directory = Path(temporary)
        archive = directory / 'catalog.tar.gz'
        download(f'https://codeload.github.com/RoaycL/Surge/tar.gz/{SNAPSHOT}', archive)
        with tarfile.open(archive) as catalog:
            def read(path):
                entry = catalog.getmember(f'Surge-{SNAPSHOT}/{path}')
                if not entry.isfile() or entry.size > 4 * 1024 * 1024:
                    raise ValueError(path)
                return catalog.extractfile(entry).read()
            manifest = json.loads(checked(read('upstreams/yfamilys/manifest.json'), MANIFEST))['modules']
            for item in manifest:
                name = Path(item['source']).name
                if item['source'] != f'upstreams/yfamilys/sources/{name}':
                    raise ValueError(name)
                (directory / name).write_bytes(checked(read(item['source']), item['sha256']))
        def script(item):
            name, digest = item
            target = directory / f'{name}.js'
            download(f'{AUTHOR}/{name}.js', target)
            checked(target.read_bytes(), digest)
        with ThreadPoolExecutor(max_workers=4) as pool:
            list(pool.map(script, SCRIPTS.items()))
        resource = directory / 'reject-dict.json'
        download(f'{AUTHOR}/reject-dict.json', resource)
        checked(resource.read_bytes(), '44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a')
        if args.live_rates:
            latest = directory / 'live-reference.json'
            address = subprocess.check_output(['curl', '--noproxy', '*', '--proto', '=https', '--ipv4', '--fail', '--silent', '--show-error',
                '--max-time', '25', '--max-filesize', '65536', '--output', latest, '--write-out', '%{remote_ip}',
                'https://api.exchangerate-api.com/v4/latest/CNY'], text=True)
            quote = json.loads(latest.read_bytes())
            roots = ssl.get_default_verify_paths().cafile
            if quote['base'] != 'CNY' or not ipaddress.ip_address(address).is_global or not roots:
                raise ValueError('Cannot independently verify the live CNY quote and its TLS route')
            (directory / 'live.json').write_text(json.dumps({'date': quote['date'], 'USD': quote['rates']['USD'], 'JPY': quote['rates']['JPY'],
                'address': address, 'roots': roots}))
        print(f'Pinned: {len(manifest)} module sources, {len(SCRIPTS)} executable scripts.', flush=True)
        overlay = directory / 'overlay.json'
        target = core / 'experimental/libbox/yfamily_catalog_test.go'
        overlay.write_text(json.dumps({'Replace': {str(target): str(Path(__file__).with_name('catalog_test.go').resolve())}}))
        command = ['go', 'test', '-json', '-mod=readonly', f'-overlay={overlay}', '-tags=with_xhttp', '-ldflags=-checklinkname=0',
                        './experimental/libbox', './experimental/httpdebug', '-run', '^(TestYFamilyCatalog|TestYFamilyInventory|TestUIModulePanels|TestUIModuleHostScope|TestUISurgeModule.*|TestSurgeRedirectFragments)$',
                        '-count=1', '-timeout=2m']
        tests, logs = [], {}
        inventory = directory / 'inventory.json'
        with subprocess.Popen(command, stdout=subprocess.PIPE, text=True, cwd=core,
                env={**os.environ, 'BBOX_SURGE_CATALOG': temporary, 'BBOX_SURGE_INVENTORY': str(inventory)}) as process:
            for line in process.stdout:
                event = json.loads(line)
                name, action, text = event.get('Test'), event['Action'], event.get('Output', '')
                if name and text:
                    logs.setdefault(name, []).append(text)
                if text and (args.verbose or action == 'build-output' or name in ('TestYFamilyInventory', 'TestYFamilyCatalog/live-rates')):
                    print(text, end='', flush=True)
                if action == 'fail' and name and not args.verbose:
                    print(''.join(logs.pop(name, [])[-8:]), end='', flush=True)
                if name and action == 'pass':
                    tests.append(name)
            if process.wait():
                raise SystemExit('Catalog replay failed; no passing report written.')
        print(f'PASS: {len(tests)} tests/subtests including the 24 original module scenarios.', flush=True)
        if args.report:
            report = {'snapshot': SNAPSHOT, 'manifestSHA256': MANIFEST, 'inventory': json.loads(inventory.read_bytes()), 'passedTests': sorted(tests)}
            if args.live_rates:
                report['liveReference'] = {'date': quote['date'], 'base': quote['base'], 'USD': quote['rates']['USD'], 'JPY': quote['rates']['JPY']}
            args.report.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')


if __name__ == '__main__':
    main()
