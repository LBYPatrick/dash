// Exercise the real source-build route with an SSH/SCP transport double.
// Builds and verifies actual payloads, without contacting any remote device.
import { execFileSync } from 'node:child_process';
import { mkdtemp, mkdir, writeFile, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import assert from 'node:assert/strict';
const root = await mkdtemp(join(tmpdir(), 'dash-source-install-'));
try {
  await mkdir(join(root, 'bin'));
  await writeFile(
    join(root, 'bin/ssh'),
    `#!/usr/bin/env python3
import os,sys,json
from pathlib import Path
root=Path(os.environ['DASH_INSTALL_TEST_ROOT'])
assert 'comma@fixture' in sys.argv
with (root/'ssh.jsonl').open('a') as log: log.write(json.dumps(sys.argv[1:])+'\\n')
if sys.argv[-2:]==['uname','-m']: print('aarch64')
else:
    script=sys.stdin.read()
    if script:
        (root/('activate.sh' if 'ACTIVATE' not in script and 'DASH_PREVIOUS=' in script else 'preflight.sh')).write_text(script)
`,
    { mode: 0o700 },
  );
  await writeFile(
    join(root, 'bin/scp'),
    `#!/usr/bin/env python3
import os,sys,shutil
from pathlib import Path
source,target=sys.argv[-2:]
assert target.startswith('comma@fixture:/data/dash/')
root=Path(os.environ['DASH_INSTALL_TEST_ROOT'])/'transfer'
root.mkdir(exist_ok=True)
if source.endswith('/.'):
    shutil.copytree(source,root/'payload',dirs_exist_ok=True)
else: shutil.copy2(source,root/target.rsplit('/',1)[-1])
`,
    { mode: 0o700 },
  );
  execFileSync(
    'bash',
    ['scripts/install-backend.sh', '--source', 'comma@fixture'],
    {
      stdio: 'inherit',
      env: {
        ...process.env,
        PATH: `${root}/bin:${process.env.PATH}`,
        DASH_INSTALL_TEST_ROOT: root,
        GITHUB_ACTIONS: '',
      },
    },
  );
  const transferred = join(root, 'transfer');
  const build = JSON.parse(
    await readFile(join(transferred, 'payload/release.json'), 'utf8'),
  );
  assert.equal(build.channel, 'local');
  assert.equal(build.arch, 'arm64');
  assert.match(build.version, /-local\.\d+\.[a-f0-9]{8}/);
  assert.equal(
    JSON.parse(await readFile(join(transferred, '.update.new'), 'utf8'))
      .automatic,
    false,
  );
  assert.match(
    await readFile(join(transferred, '.service.new'), 'utf8'),
    /dash-bootstrap --supervise/,
  );
  assert(
    (
      await readFile(join(transferred, 'payload/dist/index.html'), 'utf8')
    ).includes('<html'),
  );
  execFileSync('bash', ['-n', join(root, 'activate.sh')]);
  execFileSync('bash', ['-n', join(root, 'preflight.sh')]);
  assert.match(await readFile(join(root, 'activate.sh'), 'utf8'), /IsOnroad/);
  const calls = (await readFile(join(root, 'ssh.jsonl'), 'utf8'))
    .trim()
    .split('\n')
    .map(JSON.parse);
  assert.equal(calls.length, 4);
  console.log(
    'Source installer passed: full local checks, cross-compiled ARM64/PWA, verified payload, OTA disabled, parked activation and shared systemd unit; SSH/SCP transport was mocked, no device contacted.',
  );
} finally {
  await rm(root, { recursive: true, force: true });
}
