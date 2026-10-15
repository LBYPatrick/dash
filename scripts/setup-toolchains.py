#!/usr/bin/env python3
"""Install verified official SDKs in ignored project storage, never system paths."""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import platform
import posixpath
import re
import shlex
import shutil
import ssl
import subprocess
import tarfile
import tempfile
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
TOOLS = ROOT / '.artifacts/toolchains'
TRUSTED = {'go.dev', 'dl.google.com', 'github.com', 'release-assets.githubusercontent.com'}


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
    # Python.org macOS builds may have no CA file until their optional
    # certificate installer runs. Use the OS CA bundle, still verifying TLS.
    context = ssl.create_default_context()
    if (platform.system() == 'Darwin' and not os.environ.get('SSL_CERT_FILE')
            and not ssl.get_default_verify_paths().cafile and Path('/etc/ssl/cert.pem').is_file()):
        context = ssl.create_default_context(cafile='/etc/ssl/cert.pem')
    return urllib.request.build_opener(Redirect, urllib.request.HTTPSHandler(context=context)).open(url, timeout=60)


def metadata(url):
    with fetch(url) as response:
        data = response.read((8 << 20) + 1)
    if len(data) > 8 << 20:
        raise ValueError('SDK metadata too large')
    return data


def download_archive(url, digest, archive):
    if not re.fullmatch('[a-f0-9]{64}', digest):
        raise ValueError('Invalid official checksum')
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


def install_sdk(url, digest, name, prefix):
    TOOLS.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(dir=TOOLS) as directory:
        temporary = Path(directory)
        archive = temporary / 'sdk.tar.gz'
        download_archive(url, digest, archive)
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


def install_viteplus_cli(url, digest):
    """Extract only the verified native CLI; never execute an installer script."""
    TOOLS.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(dir=TOOLS) as directory:
        temporary = Path(directory)
        archive = temporary / 'vp.tar.gz'
        download_archive(url, digest, archive)
        with tarfile.open(archive, 'r:gz') as bundle:
            entries = [entry for entry in bundle.getmembers() if entry.name == 'vp']
            if len(entries) != 1 or not entries[0].isfile() or not 0 < entries[0].size <= 128 << 20:
                raise ValueError('Invalid Vite+ CLI archive')
            binary = temporary / 'vp'
            with bundle.extractfile(entries[0]) as source, binary.open('xb') as output:
                shutil.copyfileobj(source, output)
        binary.chmod(0o755)
        destination = TOOLS / 'vite-plus/bin/vp'
        destination.parent.mkdir(parents=True, exist_ok=True)
        binary.replace(destination)
        return destination


def provision_viteplus(required_version, host_os, arch):
    home = TOOLS / 'vite-plus'
    cli = home / 'bin/vp'
    env = {**os.environ, 'VP_HOME': str(home), 'VP_NO_UPDATE_CHECK': '1', 'VP_NODE_MANAGER': 'yes'}
    installed = version([str(cli), '--version']) if cli.is_file() else ''
    if not re.search(r'^vp v' + re.escape(required_version) + r'$', installed, re.M):
        triple = ('aarch64' if arch == 'arm64' else 'x86_64') + ('-apple-darwin' if host_os == 'darwin' else '-unknown-linux-gnu')
        base = f'https://github.com/voidzero-dev/vite-plus/releases/download/v{required_version}/'
        filename = f'vp-{triple}.tar.gz'
        checksums = metadata(base + 'vp-checksums.txt').decode()
        digest = next(line.split()[0] for line in checksums.splitlines() if line.split()[-1].lstrip('*') == filename)
        bootstrap = install_viteplus_cli(base + filename, digest)
        subprocess.run([str(bootstrap), '--help'], env=env, cwd=ROOT, check=True, stdout=subprocess.DEVNULL)
    # All configuration and downloads stay inside this project's ignored SDK
    # directory. No user defaults or shell profiles are changed.
    subprocess.run([str(cli), 'env', 'setup', '--refresh'], env=env, cwd=ROOT, check=True)
    subprocess.run([str(cli), 'env', 'on'], env=env, cwd=ROOT, check=True)
    subprocess.run([str(cli), 'env', 'install'], env=env, cwd=ROOT, check=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--check', action='store_true')
    args = parser.parse_args()
    required_go = re.search(r'^go (\d+\.\d+\.\d+)$', (ROOT / 'backend/go.mod').read_text(), re.M)[1]
    required_vp = json.loads((ROOT / 'package.json').read_text())['devDependencies']['vite-plus']
    required_node = (ROOT / '.node-version').read_text().strip()
    if not re.fullmatch(r'\d+\.\d+\.\d+', required_vp) or not re.fullmatch(r'\d+\.\d+\.\d+', required_node):
        raise SystemExit('Pin exact Vite+ and Node versions before host setup')
    node_version = version(['node', '--version']) if args.check else ''
    go_version = version(['go', 'version'])
    vp_version = version(['vp', '--version']) if args.check else ''
    go_match = re.search(r'go(\d+)\.(\d+)\.(\d+)', go_version)
    go_ok = go_match and tuple(map(int, go_match.groups())) >= tuple(map(int, required_go.split('.')))
    if args.check:
        if node_version != 'v' + required_node or not go_ok or not re.search(r'^vp v' + re.escape(required_vp) + r'$', vp_version, re.M):
            raise SystemExit(f'Require Node {required_node}, Vite+ {required_vp} and Go >={required_go}; run make setup-build-env')
        print(f'Host SDKs verified: {node_version}, Vite+ {required_vp}, {go_version}')
        return
    host_os = {'Darwin': 'darwin', 'Linux': 'linux'}.get(platform.system())
    arch = {'aarch64': 'arm64', 'arm64': 'arm64', 'x86_64': 'amd64'}.get(platform.machine())
    if not host_os or not arch:
        raise SystemExit('Supported build hosts: Linux/macOS ARM64 or AMD64')
    if not go_ok:
        releases = json.loads(metadata('https://go.dev/dl/?mode=json&include=all'))
        release = next(item for item in releases if item['version'] == 'go' + required_go)
        asset = next(item for item in release['files'] if item['os'] == host_os and item['arch'] == arch and item['kind'] == 'archive')
        install_sdk('https://go.dev/dl/' + asset['filename'], asset['sha256'], 'go', 'go')
    provision_viteplus(required_vp, host_os, arch)
    paths = [path for path in [TOOLS / 'vite-plus/bin', TOOLS / 'go/bin'] if path.is_dir()]
    env = '# Generated by Dash host setup; no shell profile changes.\n'
    env += 'export VP_HOME=' + shlex.quote(str(TOOLS / 'vite-plus')) + '\n'
    env += 'export VP_NO_UPDATE_CHECK=1\n'
    env += 'export PATH=' + shlex.quote(':'.join(str(path) for path in paths)) + ':"$PATH"\n'
    (TOOLS / 'env.sh').write_text(env)
    print('SDK setup complete: Vite+ manages the pinned Node and pnpm; compatible Go reused.')


if __name__ == '__main__':
    main()
