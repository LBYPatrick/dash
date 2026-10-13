import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
const base = `http://127.0.0.1:${process.env.DASH_SANDBOX_PORT || 8089}`;
const request = async (path, data) =>
  fetch(
    `${base}${path}`,
    data
      ? {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(data),
        }
      : {},
  );
const state = async (value) => {
  const response = await request('/api/simulation/state', { state: value });
  assert.equal(response.status, 200);
  assert.equal((await response.json()).driving, value);
};
const wait = async (predicate, timeout = 60000) => {
  const until = Date.now() + timeout;
  while (Date.now() < until) {
    if (await predicate()) return;
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  throw new Error('Sandbox check timed out');
};
const drive = '2026-10-12--17-42-08';
try {
  const info = await (await request('/api/info')).json();
  assert.equal(info.simulation, true);
  // Tests deliberately reset state; a previous interactive session may be parked.
  for (const value of ['unknown', 'onroad']) {
    await state(value);
    assert.equal(
      (await request('/api/exports', { drive, segments: [0], format: 'zip' }))
        .status,
      409,
    );
    assert.equal((await request('/api/index/rebuild', {})).status, 409);
    const index = await (await request('/api/index')).json();
    assert.equal(index.status.driving, value);
  }
  await state('parked');
  assert.equal((await request('/api/index/rebuild', {})).status, 200);
  await wait(async () => {
    const data = await (await request('/api/index')).json();
    return (
      data.status.completed === 6 &&
      !data.status.running &&
      data.status.errors === 0
    );
  });
  const index = await (await request('/api/index')).json();
  assert.equal(Object.keys(index.segments).length, 6);
  assert(
    Object.values(index.segments).every(
      (entry) => entry.thumbnail && entry.metadata.car === 'TEST VEHICLE',
    ),
  );
  const preview = await request(`/api/video?file=${drive}--0/qcamera.ts`);
  assert.equal(preview.status, 200);
  assert((await preview.arrayBuffer()).byteLength > 1000);
  for (const format of ['zip', 'mp4', 'mcap']) {
    const response = await request('/api/exports', {
      drive,
      segments: [0, 1],
      format,
      camera: 'qcamera.ts',
    });
    assert.equal(response.status, 202);
    const job = await response.json();
    await wait(async () => {
      const status = await (await request(`/api/exports/${job.id}`)).json();
      if (status.status === 'failed' || status.status === 'canceled')
        throw new Error(status.message);
      return status.status === 'complete';
    });
    const download = await request(`/api/exports/${job.id}/download`);
    assert.equal(download.status, 200);
    assert((await download.arrayBuffer()).byteLength > 100);
  }
  const diagnostic = await request('/api/diagnostics/download');
  assert.equal(diagnostic.status, 200);
  const report = await diagnostic.text();
  assert(report.includes('Simulation: true'));
  assert(report.includes('verified container limits'));
  assert(report.includes('memory.max: 402653184'));
  const pid = execFileSync('docker', ['compose', 'ps', '-q', 'dash'], {
    encoding: 'utf8',
  }).trim();
  const inspect = JSON.parse(
    execFileSync('docker', ['inspect', pid], { encoding: 'utf8' }),
  )[0];
  assert.equal(inspect.HostConfig.NanoCpus, 500000000);
  assert.equal(inspect.HostConfig.Memory, 384 * 1024 * 1024);
  assert.equal(inspect.HostConfig.MemorySwap, 384 * 1024 * 1024);
  assert.equal(inspect.HostConfig.PidsLimit, 64);
  assert.equal(inspect.HostConfig.Privileged, false);
  assert.equal(inspect.HostConfig.ReadonlyRootfs, true);
  const unbounded = spawnSync(
    'docker',
    [
      'run',
      '--rm',
      '--network',
      'none',
      '--read-only',
      '--tmpfs',
      '/tmp:size=16m,mode=1777',
      '--entrypoint',
      'dash-backend',
      inspect.Config.Image,
      '--cache',
      '/tmp',
      '--require-container-limits',
    ],
    { encoding: 'utf8' },
  );
  assert.equal(unbounded.status, 1);
  assert.match(unbounded.stderr, /container resource verification: CPU quota/);
  execFileSync('docker', ['compose', 'restart', 'dash'], { stdio: 'pipe' });
  await wait(async () => {
    try {
      return (await request('/api/info')).ok;
    } catch {
      return false;
    }
  });
  assert.equal((await (await request('/api/info')).json()).driving, 'unknown');
  const retained = await (await request('/api/diagnostics/download')).text();
  assert(retained.includes('format=zip'));
  await state('unknown');
  console.log(
    'Sandbox passed: enforced CPU/memory/swap/PID limits, fail-closed driving state, incremental index/thumbnails, native playback, ZIP/MP4/MCAP, and diagnostic download. Left in unknown state.',
  );
} finally {
  await state('unknown').catch(() => {});
}
