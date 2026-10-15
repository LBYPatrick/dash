import { createHash } from 'node:crypto';
import { mkdir, readFile, writeFile, cp, stat } from 'node:fs/promises';
import { execFileSync } from 'node:child_process';
import { resolve, join } from 'node:path';
import { fileURLToPath } from 'node:url';
const digest = (data) => createHash('sha256').update(data).digest('hex');
export async function buildMedia(arch) {
  if (!['arm64', 'amd64'].includes(arch))
    throw new Error('Unsupported media architecture');
  const sources = JSON.parse(
    await readFile('tools/media/sources.json', 'utf8'),
  );
  const inputs = digest(
    Buffer.concat(
      await Promise.all(
        [
          'tools/media/sources.json',
          'tools/media/build.sh',
          'tools/media/Dockerfile',
        ].map((path) => readFile(path)),
      ),
    ),
  );
  const output = resolve(`.artifacts/media/linux-${arch}`);
  try {
    const manifest = JSON.parse(
      await readFile(join(output, 'manifest.json'), 'utf8'),
    );
    if (
      manifest.inputs === inputs &&
      manifest.arch === arch &&
      (await Promise.all(
        ['ffmpeg', 'ffprobe'].map(
          async (name) =>
            digest(await readFile(join(output, name))) ===
            manifest.binaries[name].sha256,
        ),
      ).then((values) => values.every(Boolean)))
    )
      return output;
  } catch {
    /* Build missing or stale artifacts; never trust an unchecked cache. */
  }
  const cache = resolve('.artifacts/media-source');
  await mkdir(cache, { recursive: true });
  for (const source of Object.values(sources)) {
    const path = join(cache, source.file);
    let valid = false;
    try {
      valid = digest(await readFile(path)) === source.sha256;
    } catch {}
    if (!valid) {
      execFileSync(
        'curl',
        [
          '--fail',
          '--location',
          '--proto',
          '=https',
          '--tlsv1.2',
          '--retry',
          '2',
          '--connect-timeout',
          '10',
          '--max-time',
          '180',
          source.url,
          '-o',
          path,
        ],
        { stdio: 'inherit' },
      );
      if (digest(await readFile(path)) !== source.sha256)
        throw new Error(`Media source checksum mismatch: ${source.file}`);
    }
  }
  execFileSync(
    'docker',
    [
      'buildx',
      'build',
      '--platform',
      `linux/${arch}`,
      '--build-context',
      `sources=${cache}`,
      '-f',
      'tools/media/Dockerfile',
      '--target',
      'bundle',
      '--output',
      `type=local,dest=${output}`,
      '.',
    ],
    { stdio: 'inherit' },
  );
  await cp('tools/media/Dockerfile', join(output, 'sources/Dockerfile'));
  await cp('tools/media/sources.json', join(output, 'sources/sources.json'));
  const binaries = {};
  for (const name of ['ffmpeg', 'ffprobe']) {
    const data = await readFile(join(output, name));
    if (
      data.subarray(0, 4).toString() !== '\x7fELF' ||
      data[4] !== 2 ||
      data.readUInt16LE(18) !== (arch === 'arm64' ? 183 : 62)
    )
      throw new Error(`Wrong media executable architecture: ${name}`);
    binaries[name] = {
      bytes: (await stat(join(output, name))).size,
      sha256: digest(data),
    };
  }
  await writeFile(
    join(output, 'manifest.json'),
    JSON.stringify(
      {
        protocol: 1,
        arch,
        inputs,
        ffmpeg: sources.ffmpeg.version,
        x264: sources.x264.version,
        binaries,
      },
      null,
      2,
    ) + '\n',
  );
  return output;
}
if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  const args = process.argv.slice(2);
  for (const arch of args.length ? args : ['arm64', 'amd64'])
    console.log(`Media bundle: ${await buildMedia(arch)}`);
}
