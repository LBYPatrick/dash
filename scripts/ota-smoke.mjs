// A real Linux bootstrap/child/rollback exercise using local CI-format bundles.
// It never contacts or changes a vehicle, GitHub releases, or driving firmware.
import { execFileSync, spawnSync } from 'node:child_process';
import {
  mkdir,
  cp,
  readFile,
  writeFile,
  rm,
  chmod,
  readlink,
  rename,
  appendFile,
} from 'node:fs/promises';
import { resolve, join } from 'node:path';
import assert from 'node:assert/strict';
const root = resolve('.artifacts/ota-sandbox');
const docker = (...args) =>
  execFileSync('docker', args, { encoding: 'utf8' }).trim();
const image = JSON.parse(docker('image', 'inspect', 'dash-sandbox-dash'))[0];
const arch = image.Architecture;
assert(['arm64', 'amd64'].includes(arch));
await rm(root, { recursive: true, force: true });
await mkdir(join(root, 'releases'), { recursive: true });
await writeFile('.artifacts/ota-sandbox.log', '');
await mkdir(join(root, 'cache'));
await mkdir(join(root, 'params'));
await writeFile(join(root, 'params/IsOnroad'), '0');
await writeFile(join(root, 'params/IsOffroad'), '1');
await writeFile(
  join(root, 'update.json'),
  JSON.stringify({
    repository: 'LBYPatrick/dash',
    channel: 'dev',
    automatic: false,
  }),
);
await writeFile(join(root, 'cache/ota-sentinel'), 'keep this cache');
const manifests = [];
for (const sequence of ['1', '2']) {
  execFileSync('node', ['scripts/release.mjs'], {
    stdio: 'inherit',
    env: {
      ...process.env,
      GITHUB_REF_NAME: '',
      GITHUB_RUN_NUMBER: '',
      DASH_RELEASE_CHANNEL: 'dev',
      DASH_RELEASE_SEQUENCE: sequence,
    },
  });
  const manifest = JSON.parse(
    await readFile('.artifacts/release/manifest.json', 'utf8'),
  );
  const target = join(root, 'releases', manifest.tag);
  await mkdir(target);
  execFileSync('tar', [
    '-xzf',
    resolve('.artifacts/release', manifest.assets[arch].name),
    '-C',
    target,
  ]);
  manifests.push(manifest);
}
const [first, second] = manifests;
// A local source build uses the same native bootstrap layout, but must never
// inherit OTA even when the previous CI installation configured a channel.
execFileSync('node', ['scripts/release.mjs', '--source'], {
  stdio: 'inherit',
  env: {
    ...process.env,
    GITHUB_ACTIONS: '',
    DASH_RELEASE_ARCH: arch,
    DASH_RELEASE_SEQUENCE: '123456',
  },
});
const local = JSON.parse(
  await readFile('.artifacts/release/manifest.json', 'utf8'),
);
const localTarget = join(root, 'releases', local.tag);
await mkdir(localTarget);
execFileSync('python3', [
  'scripts/fetch-release.py',
  '--local-manifest',
  '.artifacts/release/manifest.json',
  '--arch',
  arch,
  '--destination',
  join(root, 'verified-local'),
]);
await rm(localTarget, { recursive: true });
await rename(join(root, 'verified-local'), localTarget);
await cp(
  join(root, 'releases', first.tag, 'dash-backend'),
  join(root, 'dash-bootstrap'),
);
await chmod(join(root, 'dash-bootstrap'), 0o700);
execFileSync('ln', ['-s', `releases/${first.tag}`, join(root, 'current')]);
await writeFile(join(root, 'ready.json'), JSON.stringify(second));
async function saveLogs(id) {
  const result = spawnSync('docker', ['logs', id], { encoding: 'utf8' });
  await appendFile('.artifacts/ota-sandbox.log', result.stdout + result.stderr);
}
const containerName = `dash-ota-test-${process.pid}`;
let container;
const wait = async (predicate, timeout = 60000) => {
  const end = Date.now() + timeout;
  while (Date.now() < end) {
    if (await predicate()) return;
    await new Promise((r) => setTimeout(r, 250));
  }
  throw new Error('OTA sandbox timed out');
};
try {
  container = docker(
    'run',
    '-d',
    '--rm',
    '--name',
    containerName,
    '--init',
    '--read-only',
    '--cap-drop=ALL',
    '--security-opt=no-new-privileges:true',
    '--cpus=0.5',
    '--memory=384m',
    '--memory-swap=384m',
    '--pids-limit=64',
    '--user',
    `${process.getuid()}:${process.getgid()}`,
    '-v',
    `${root}:/data/dash`,
    '-p',
    '127.0.0.1::8088',
    '--entrypoint',
    '/data/dash/dash-bootstrap',
    image.Id,
    '--supervise',
    '--update-home',
    '/data/dash',
    '--require-container-limits',
    '--simulation',
    '--listen',
    '0.0.0.0:8088',
    '--root',
    '/recordings',
    '--cache',
    '/data/dash/cache',
    '--params',
    '/data/dash/params',
  );
  const port = JSON.parse(docker('inspect', container))[0].NetworkSettings
    .Ports['8088/tcp'][0].HostPort;
  let base = `http://127.0.0.1:${port}`;
  const get = async (path) => {
    try {
      return await (
        await fetch(base + path, { signal: AbortSignal.timeout(2500) })
      ).json();
    } catch {
      return {};
    }
  };
  const post = (path, data) =>
    fetch(base + path, {
      signal: AbortSignal.timeout(5000),
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(data),
    });
  await wait(async () => (await get('/api/info')).build?.tag === first.tag);
  assert.equal((await get('/api/updates')).phase, 'ready');
  assert.equal((await post('/api/updates', { action: 'install' })).status, 202);
  await wait(async () => {
    const status = await get('/api/updates');
    return status.current?.tag === second.tag && !status.pendingHealth;
  });
  assert.equal(await readlink(join(root, 'current')), `releases/${second.tag}`);
  assert.equal(await readlink(join(root, 'previous')), `releases/${first.tag}`);
  await wait(async () => {
    try {
      return (
        JSON.parse(
          await readFile(join(root, 'cache/supervisor-ready.json'), 'utf8'),
        ).tag === second.tag
      );
    } catch {
      return false;
    }
  });
  assert.equal(
    await readFile(join(root, 'cache/ota-sentinel'), 'utf8'),
    'keep this cache',
  );
  // Stage an ELF with the right architecture but a deliberately failing entry
  // point. A checksum-valid release can still fail to start; that is why the
  // supervisor must retain a working copy and test the new process itself.
  const bad = structuredClone(second);
  bad.sequence = 3;
  bad.version = second.version.replace('-dev.2.', '-dev.3.');
  bad.tag = `v${bad.version}`;
  const broken = join(root, 'releases', bad.tag);
  await cp(join(root, 'releases', second.tag), broken, { recursive: true });
  const falseBinary = execFileSync('docker', [
    'run',
    '--rm',
    '--entrypoint',
    '/bin/cat',
    image.Id,
    '/bin/false',
  ]);
  await writeFile(join(broken, 'dash-backend'), falseBinary);
  await chmod(join(broken, 'dash-backend'), 0o700);
  const { assets, ...build } = bad;
  await writeFile(
    join(broken, 'release.json'),
    JSON.stringify({ ...build, arch }),
  );
  bad.assets[arch].name = `dash-${bad.tag}-linux-${arch}.tar.gz`;
  await writeFile(join(root, 'ready.json'), JSON.stringify(bad));
  await post('/api/simulation/state', { state: 'unknown' });
  docker('restart', container);
  const restartedPort = JSON.parse(docker('inspect', container))[0]
    .NetworkSettings.Ports['8088/tcp'][0].HostPort;
  base = `http://127.0.0.1:${restartedPort}`;
  await wait(async () => (await get('/api/info')).build?.tag === second.tag);
  assert.equal((await post('/api/updates', { action: 'install' })).status, 409);
  assert.equal((await get('/api/index')).status.running, false);
  const children = docker(
    'exec',
    container,
    'sh',
    '-c',
    'for item in /proc/[0-9]*/comm; do cat "$item"; done',
  );
  assert(
    !children.split('\n').includes('ffmpeg'),
    'Unknown state must leave no FFmpeg worker',
  );
  await post('/api/simulation/state', { state: 'parked' });
  assert.equal((await post('/api/updates', { action: 'install' })).status, 202);
  await wait(async () => {
    const status = await get('/api/updates');
    return (
      status.current?.tag === second.tag && status.message?.includes('restored')
    );
  });
  assert.equal(await readlink(join(root, 'current')), `releases/${second.tag}`);
  assert.equal(
    await readFile(join(root, 'cache/ota-sentinel'), 'utf8'),
    'keep this cache',
  );
  // A corrupt updater configuration disables OTA, not the recording library.
  await post('/api/simulation/state', { state: 'unknown' });
  await writeFile(join(root, 'update.json'), '{broken');
  docker('restart', container);
  const recoveryPort = JSON.parse(docker('inspect', container))[0]
    .NetworkSettings.Ports['8088/tcp'][0].HostPort;
  base = `http://127.0.0.1:${recoveryPort}`;
  await wait(async () => (await get('/api/info')).build?.tag === second.tag);
  assert.equal((await get('/api/updates')).managed, false);
  assert.match((await get('/api/updates')).message, /unavailable/);
  assert.equal((await post('/api/updates', { action: 'check' })).status, 409);
  await saveLogs(container);
  docker('stop', '-t', '5', container);
  // --rm removes the container on stop; launch an equally limited container.
  container = undefined;
  // The source build is exercised independently by a second limited container.
  const sourceRoot = resolve('.artifacts/source-sandbox');
  await rm(sourceRoot, { recursive: true, force: true });
  await mkdir(join(sourceRoot, 'releases'), { recursive: true });
  await cp(localTarget, join(sourceRoot, 'releases', local.tag), {
    recursive: true,
  });
  await cp(join(root, 'dash-bootstrap'), join(sourceRoot, 'dash-bootstrap'));
  await mkdir(join(sourceRoot, 'cache'));
  await cp(join(root, 'params'), join(sourceRoot, 'params'), {
    recursive: true,
  });
  await writeFile(
    join(sourceRoot, 'update.json'),
    JSON.stringify({
      repository: 'LBYPatrick/dash',
      channel: 'main',
      automatic: true,
    }),
  );
  execFileSync('ln', [
    '-s',
    `releases/${local.tag}`,
    join(sourceRoot, 'current'),
  ]);
  container = docker(
    'run',
    '-d',
    '--rm',
    '--name',
    containerName,
    '--init',
    '--read-only',
    '--cap-drop=ALL',
    '--security-opt=no-new-privileges:true',
    '--cpus=0.5',
    '--memory=384m',
    '--memory-swap=384m',
    '--pids-limit=64',
    '--user',
    `${process.getuid()}:${process.getgid()}`,
    '-v',
    `${sourceRoot}:/data/dash`,
    '-p',
    '127.0.0.1::8088',
    '--entrypoint',
    '/data/dash/dash-bootstrap',
    image.Id,
    '--supervise',
    '--update-home',
    '/data/dash',
    '--require-container-limits',
    '--simulation',
    '--listen',
    '0.0.0.0:8088',
    '--root',
    '/recordings',
    '--cache',
    '/data/dash/cache',
    '--params',
    '/data/dash/params',
  );
  base = `http://127.0.0.1:${JSON.parse(docker('inspect', container))[0].NetworkSettings.Ports['8088/tcp'][0].HostPort}`;
  await wait(async () => (await get('/api/info')).build?.tag === local.tag);
  await wait(async () => {
    try {
      return (
        JSON.parse(
          await readFile(
            join(sourceRoot, 'cache/supervisor-ready.json'),
            'utf8',
          ),
        ).tag === local.tag
      );
    } catch {
      return false;
    }
  });
  assert.equal((await get('/api/updates')).managed, false);
  await post('/api/simulation/state', { state: 'parked' });
  assert.equal((await post('/api/updates', { action: 'check' })).status, 409);
  const inspect = JSON.parse(docker('inspect', container))[0];
  assert.equal(inspect.State.OOMKilled, false);
  assert.equal(inspect.HostConfig.Memory, 384 * 1024 * 1024);
  assert.equal(inspect.HostConfig.NanoCpus, 500000000);
  const events = docker(
    'exec',
    container,
    'cat',
    '/sys/fs/cgroup/memory.events',
  );
  assert.match(events, /oom_kill 0/);
  await saveLogs(container);
  console.log(
    'OTA sandbox passed: CI-format native/PWA bundles, supervisor activation, 10-second health probation, previous-release retention, cache persistence, unknown-state rejection/no workers, failed-child rollback, corrupt-config recovery, local-source boot with OTA disabled, process cleanup, and enforced CPU/memory limits without OOM.',
  );
} finally {
  if (container) {
    try {
      await saveLogs(container);
    } catch {}
    spawnSync('docker', ['stop', '-t', '5', container], { stdio: 'ignore' });
  }
}
