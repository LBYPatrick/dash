// Validate the exact release tools on Linux, without device access or recordings.
import { execFileSync } from 'node:child_process';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { resolve, join } from 'node:path';
import { buildMedia } from './build-media.mjs';
const root = await mkdtemp(resolve('.artifacts/media-smoke-'));
const run = (tool, args, options = {}) =>
  execFileSync(tool, args, { stdio: 'pipe', ...options });
try {
  for (const [name, encoder, format] of [
    ['preview.ts', 'libx264', 'mpegts'],
    ['road.hevc', 'libx265', 'hevc'],
  ]) {
    run('ffmpeg', [
      '-v',
      'error',
      '-f',
      'lavfi',
      '-i',
      'testsrc2=s=320x180:r=20',
      '-t',
      '0.5',
      '-an',
      '-c:v',
      encoder,
      '-threads',
      '1',
      '-preset',
      'ultrafast',
      '-pix_fmt',
      'yuv420p',
      '-bf',
      '0',
      ...(encoder === 'libx265'
        ? ['-x265-params', 'pools=none:frame-threads=1:log-level=error']
        : []),
      '-f',
      format,
      join(root, name),
    ]);
  }
  await writeFile(
    join(root, 'verify.sh'),
    `set -eu
/media/ffmpeg -v error -nostdin -y -max_alloc 33554432 -threads 8 -filter_threads 8 -i /test/preview.ts -frames:v 1 -vf scale=160:-2 /test/thumb.jpg
/media/ffmpeg -v error -nostdin -y -max_alloc 33554432 -threads 8 -i /test/preview.ts -c:v copy -an /test/preview.mp4
/media/ffmpeg -v error -nostdin -y -max_alloc 33554432 -threads 8 -filter_threads 8 -r 20 -i /test/road.hevc -an -c:v libx264 -threads 8 -preset ultrafast -bf 0 -pix_fmt yuv420p -vf scale=160:-2 -f mpegts /test/converted.ts
/media/ffmpeg -v error -nostdin -y -i /test/preview.ts -an -c:v copy -bsf:v h264_metadata=aud=insert -f h264 /test/video.h264
/media/ffmpeg -v error -nostdin -y -i /test/road.hevc -an -c:v copy -bsf:v hevc_metadata=aud=insert -f hevc /test/video.hevc
for file in preview.mp4 converted.ts video.h264 video.hevc; do
  /media/ffprobe -v error -threads 1 -count_packets -select_streams v:0 -show_entries stream=nb_read_packets -of csv=p=0 "/test/$file" | grep -qx 10
done
test -s /test/thumb.jpg
`,
  );
  for (const arch of process.argv.length > 2
    ? process.argv.slice(2)
    : ['arm64', 'amd64']) {
    const media = await buildMedia(arch);
    try {
      run('docker', [
        'run',
        '--rm',
        '--network',
        'none',
        '--platform',
        `linux/${arch}`,
        '--memory',
        '384m',
        '--memory-swap',
        '384m',
        '--pids-limit',
        '128',
        '-v',
        `${media}:/media:ro`,
        '-v',
        `${root}:/test`,
        'alpine:3.23',
        'sh',
        '/test/verify.sh',
      ]);
    } catch (error) {
      throw new Error(
        `Bundled ${arch} media failed: ${error.stderr?.toString() || error.message}`,
      );
    }
    console.log(
      `${arch}: static release FFmpeg/ffprobe passed thumbnails, MP4, HEVC preview, H.264/H.265 MCAP access units and frame counts within 384 MiB.`,
    );
  }
} finally {
  await rm(root, { recursive: true, force: true });
}
