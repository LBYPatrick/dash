import { mkdir, copyFile, writeFile } from 'node:fs/promises';
import { execFileSync } from 'node:child_process';
import { retimeFixture } from './fixture-clock.mjs';
const template = execFileSync('bzip2', [
  '-dc',
  'dash-backend/internal/telemetry/testdata/qlog.bz2',
]);
const root = '.artifacts/device';
await mkdir(`${root}/params`, { recursive: true });
await writeFile(`${root}/params/IsOnroad`, '0');
await writeFile(`${root}/params/IsOffroad`, '1');
for (const id of [
  '2026-10-12--17-42-08',
  '2026-10-12--08-16-32',
  '2026-10-11--16-05-11',
]) {
  for (let segment = 0; segment < 2; segment++) {
    const path = `${root}/recordings/${id}--${segment}`;
    await mkdir(path, { recursive: true });
    const utc = Date.parse(
      id.replace('--', 'T').replace(/T(\d+)-(\d+)-(\d+)/, 'T$1:$2:$3') + 'Z',
    );
    await writeFile(
      `${path}/qlog.bz2`,
      execFileSync('bzip2', ['-c'], {
        input: retimeFixture(template, utc + segment * 11000, segment * 11),
      }),
    );
    execFileSync('ffmpeg', [
      '-hide_banner',
      '-loglevel',
      'error',
      '-y',
      '-i',
      'public/sample-road.mp4',
      '-c:v',
      'copy',
      '-an',
      '-f',
      'mpegts',
      `${path}/qcamera.ts`,
    ]);
    execFileSync(
      'ffmpeg',
      [
        '-hide_banner',
        '-loglevel',
        'error',
        '-y',
        '-i',
        'public/sample-road.mp4',
        '-t',
        '2',
        '-c:v',
        'libx265',
        '-threads',
        '1',
        '-x265-params',
        'pools=1:frame-threads=1:log-level=error',
        '-an',
        '-f',
        'hevc',
        `${path}/fcamera.hevc`,
      ],
      { stdio: 'ignore' },
    );
    await copyFile(`${path}/fcamera.hevc`, `${path}/ecamera.hevc`);
  }
}
console.log(
  'Local device fixture ready. These are synthetic recordings, not road data.',
);
