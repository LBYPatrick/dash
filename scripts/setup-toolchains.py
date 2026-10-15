#!/usr/bin/env python3
"""Install verified official SDKs in ignored project storage, never system paths."""
import argparse
import hashlib
import json
from pathlib import Path, PurePosixPath
import platform
import posixpath
import re
import shlex
import shutil
import subprocess
import tarfile
import tempfile
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
TOOLS = ROOT / '.artifacts/toolchains'
TRUSTED = {'go.dev', 'dl.google.com', 'nodejs.org'}


def trusted_url(url):
    parsed = urllib.parse.urlparse(url)
    if (parsed.scheme != 'https' or parsed.hostname not in TRUSTED
            or parsed.username or parsed.password or parsed.port):
        raise ValueError('Untrusted SDK URL')


class Redirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        trusted_url(newurl)
        return super().redirect_request(req, fp, code, msg, headers, newurl)


def fetch(url):
    trusted_url(url)
    return urllib.request.build_opener(Redirect).open(url, timeout=60)


def metadata(url):
    with fetch(url) as response:
        data = response.read((8 << 20) + 1)
    if len(data) > 8 << 20:
        raise ValueError('SDK metadata too large')
    return data


def install_sdk(url, digest, name, prefix):
    if not re.fullmatch('[a-f0-9]{64}', digest):
        raise ValueError('Invalid official checksum')
    TOOLS.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(dir=TOOLS) as directory:
        temporary = Path(directory)
        archive = temporary / 'sdk.tar.gz'
        checksum, size = hashlib.sha256(), 0
        with fetch(url) as source, archive.open('xb') as output:
            while chunk := source.read(128 << 10):
                size += len(chunk)
                if size > 256 << 20:
                    raise ValueError('SDK download too large')
                output.write(chunk)
                checksum.update(chunk)
        if checksum.hexdigest() != digest:
            raise ValueError('Official SDK checksum mismatch')
        with tarfile.open(archive, 'r:gz') as bundle:
            entries = bundle.getmembers()
            links, seen, expanded = set(), set(), 0
            for entry in entries:
                path = PurePosixPath(entry.name)
                expanded += entry.size
                if (path.is_absolute() or '..' in path.parts or '\\' in entry.name
                        or not path.parts or path.parts[0] != prefix
                        or entry.name in seen or len(entries) > 100000
                        or expanded > 1 << 30
                        or not (entry.isdir() or entry.isfile() or entry.issym())):
                    raise ValueError('Unsafe SDK archive')
                seen.add(entry.name)
                if entry.issym():
                    target = posixpath.normpath(posixpath.join(str(path.parent), entry.linkname))
                    if not target.startswith(prefix + '/'):
                        raise ValueError('SDK symlink escapes archive')
                    links.add(path)
            if any(parent in links for entry in entries for parent in PurePosixPath(entry.name).parents):
                raise ValueError('SDK archive writes through symlink')
            bundle.extractall(temporary)
        target = TOOLS / name
        if target.exists():
            shutil.rmtree(target)
        (temporary / prefix).rename(target)
        return target / 'bin'


def version(command):
    try:
        return subprocess.check_output(command, text=True, stderr=subprocess.DEVNULL).strip()
    except (OSError, subprocess.CalledProcessError):
        return ''


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true')
    args = parser.parse_args()
    required_go = re.search(r'^go (\d+\.\d+\.\d+)$', (ROOT / 'backend/go.mod').read_text(), re.M)[1]
    node_version = version(['node', '--version'])
    go_version = version(['go', 'version'])
    node_match = re.fullmatch(r'v(\d+)\.(\d+)\.(\d+)', node_version)
    go_match = re.search(r'go(\d+)\.(\d+)\.(\d+)', go_version)
    node_ok = node_match and tuple(map(int, node_match.groups())) >= (22, 12, 0) and bool(shutil.which('npm'))
    go_ok = go_match and tuple(map(int, go_match.groups())) >= tuple(map(int, required_go.split('.')))
    if args.check:
        if not node_ok or not go_ok:
            raise SystemExit(f'Require Node >=22.12 with npm and Go >={required_go}; run make setup-build-env')
        print(f'Host SDKs verified: {node_version}, {go_version}')
        return
    host_os = {'Darwin': 'darwin', 'Linux': 'linux'}.get(platform.system())
    arch = {'aarch64': 'arm64', 'arm64': 'arm64', 'x86_64': 'amd64'}.get(platform.machine())
    if not host_os or not arch:
        raise SystemExit('Supported build hosts: Linux/macOS ARM64 or AMD64')
    paths = []
    if not go_ok:
        releases = json.loads(metadata('https://go.dev/dl/?mode=json&include=all'))
        release = next(item for item in releases if item['version'] == 'go' + required_go)
        asset = next(item for item in release['files'] if item['os'] == host_os and item['arch'] == arch and item['kind'] == 'archive')
        paths.append(install_sdk('https://go.dev/dl/' + asset['filename'], asset['sha256'], 'go', 'go'))
    if not node_ok:
        releases = json.loads(metadata('https://nodejs.org/dist/index.json'))
        release = next(item for item in releases if re.fullmatch(r'v22\.\d+\.\d+', item['version']))
        node_arch = 'x64' if arch == 'amd64' else arch
        prefix = f"node-{release['version']}-{host_os}-{node_arch}"
        filename = prefix + '.tar.gz'
        base = f"https://nodejs.org/dist/{release['version']}/"
        checksums = metadata(base + 'SHASUMS256.txt').decode()
        digest = next(line.split()[0] for line in checksums.splitlines() if line.split()[-1] == filename)
        paths.append(install_sdk(base + filename, digest, 'node', prefix))
    # Include previous project SDKs as well when this setup is rerun.
    paths = [path for path in [TOOLS / 'node/bin', TOOLS / 'go/bin'] if path.is_dir()]
    TOOLS.mkdir(parents=True, exist_ok=True)
    env = '# Generated by Dash host setup; no shell profile changes.\n'
    if paths:
        env += 'export PATH=' + shlex.quote(':'.join(str(path) for path in paths)) + ':"$PATH"\n'
    (TOOLS / 'env.sh').write_text(env)
    print('SDK setup complete (existing compatible SDKs reused).')


if __name__ == '__main__':
    main()
