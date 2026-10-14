import { mkdir, copyFile, writeFile, readFile, rm } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';

const manifest = JSON.parse(
  await readFile('scripts/fixtures/manifest.json', 'utf8'),
);
const root = '.artifacts/device';
await mkdir(`${root}/params`, { recursive: true });
await writeFile(`${root}/params/IsOnroad`, '0');
await writeFile(`${root}/params/IsOffroad`, '1');
// Build each short, low-resolution synthetic media part once. Real devices use
// much higher resolutions; the simulator exercises format/timing, not bandwidth.
for (const duration of manifest.segmentDurationsSeconds) {
  const base = `${root}/fixture-media/${duration}`;
  await mkdir(base, { recursive: true });
  const input = [
    '-hide_banner',
    '-loglevel',
    'error',
    '-y',
    '-stream_loop',
    '-1',
    '-threads',
    '1',
    '-i',
    'public/sample-road.mp4',
    '-t',
    String(duration),
    '-r',
    String(manifest.fps),
    '-vf',
    'scale=480:-2',
    '-filter_threads',
    '1',
    '-an',
  ];
  execFileSync(
    'ffmpeg',
    [
      ...input,
      '-c:v',
      'libx264',
      '-threads',
      '1',
      '-preset',
      'ultrafast',
      '-bf',
      '0',
      '-g',
      '20',
      '-pix_fmt',
      'yuv420p',
      '-f',
      'mpegts',
      `${base}/qcamera.ts`,
    ],
    { stdio: 'inherit' },
  );
  execFileSync(
    'ffmpeg',
    [
      ...input,
      '-c:v',
      'libx265',
      '-threads',
      '1',
      '-preset',
      'ultrafast',
      '-x265-params',
      'pools=none:frame-threads=1:log-level=error:bframes=0:keyint=20',
      '-pix_fmt',
      'yuv420p',
      '-f',
      'hevc',
      `${base}/fcamera.hevc`,
    ],
    { stdio: 'inherit' },
  );
}
for (const id of [
  '2026-10-12--17-42-08',
  '2026-10-12--08-16-32',
  '2026-10-11--16-05-11',
]) {
  for (const segment of [0, 1])
    await rm(`${root}/recordings/${id}--${segment}`, {
      recursive: true,
      force: true,
    });
}
for (const id of manifest.drives) {
  for (const [
    segment,
    duration,
  ] of manifest.segmentDurationsSeconds.entries()) {
    const directory = `${id}--${segment}`;
    const path = `${root}/recordings/${directory}`;
    await mkdir(path, { recursive: true });
    for (const filename of ['qlog.bz2', 'qlog', 'rlog.bz2', 'rlog'])
      await rm(`${path}/${filename}`, { force: true });
    for (const kind of ['qlog', 'rlog']) {
      const name = `device/${directory}/${kind}.zst`;
      const data = await readFile(`scripts/fixtures/${name}`);
      if (
        createHash('sha256').update(data).digest('hex') !==
        manifest.files[name].sha256
      )
        throw new Error(`Fixture checksum mismatch: ${name}`);
      await writeFile(`${path}/${kind}.zst`, data);
    }
    const media = `${root}/fixture-media/${duration}`;
    await copyFile(`${media}/qcamera.ts`, `${path}/qcamera.ts`);
    // Deliberately identical synthetic images, not independently recorded views.
    for (const camera of ['fcamera.hevc', 'ecamera.hevc', 'dcamera.hevc'])
      await copyFile(`${media}/fcamera.hevc`, `${path}/${camera}`);
  }
}
console.log(
  'Synthetic device ready: native zstd cereal logs, 60 + 8 seconds, 20 fps, full-rate rlog and decimated qlog.',
);
