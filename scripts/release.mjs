import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import {
  mkdir,
  readFile,
  writeFile,
  readdir,
  cp,
  rm,
  stat,
} from 'node:fs/promises';
import { resolve, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { buildMedia } from './build-media.mjs';

export function releaseIdentity(channel, sequence, revision, baseVersion) {
  if (!['main', 'nightly', 'dev'].includes(channel))
    throw new Error('Releases are built only from main, nightly, or dev.');
  if (!/^[1-9]\d*$/.test(sequence) || !Number.isSafeInteger(Number(sequence)))
    throw new Error('A positive CI build number is required.');
  if (!/^[a-f0-9]{40}$/.test(revision) || !/^\d+\.\d+\.\d+$/.test(baseVersion))
    throw new Error('Invalid release revision or base version.');
  const version = `${baseVersion}${channel === 'main' ? '+' : '-'}${channel}.${sequence}.${revision.slice(0, 8)}`;
  return {
    protocol: 1,
    version,
    tag: `v${version}`,
    channel,
    sequence: Number(sequence),
    revision,
  };
}

export function sourceIdentity(sequence, revision, baseVersion, dirty) {
  if (
    !/^[1-9]\d*$/.test(sequence) ||
    !Number.isSafeInteger(Number(sequence)) ||
    !/^[a-f0-9]{40}$/.test(revision) ||
    !/^\d+\.\d+\.\d+$/.test(baseVersion)
  )
    throw new Error('Invalid local build identity');
  const version = `${baseVersion}-local.${sequence}.${revision.slice(0, 8)}${dirty ? '.dirty' : ''}`;
  return {
    protocol: 1,
    version,
    tag: `v${version}`,
    channel: 'local',
    sequence: Number(sequence),
    revision,
  };
}

async function main() {
  const pkg = JSON.parse(await readFile('package.json', 'utf8'));
  const source = process.argv.includes('--source');
  if (source && process.env.GITHUB_ACTIONS === 'true')
    throw new Error('Local source builds are not channel CI releases');
  const channel = source
    ? 'local'
    : process.env.GITHUB_REF_NAME || process.env.DASH_RELEASE_CHANNEL;
  const sequence = source
    ? process.env.DASH_RELEASE_SEQUENCE || String(Date.now())
    : process.env.GITHUB_RUN_NUMBER || process.env.DASH_RELEASE_SEQUENCE;
  const revision =
    (!source && process.env.GITHUB_SHA) ||
    execFileSync('git', ['rev-parse', 'HEAD'], { encoding: 'utf8' }).trim();
  const identity = source
    ? sourceIdentity(
        sequence,
        revision,
        pkg.version,
        !!execFileSync('git', ['status', '--porcelain'], {
          encoding: 'utf8',
        }).trim(),
      )
    : releaseIdentity(channel, sequence, revision, pkg.version);
  const output = resolve('.artifacts/release');
  await rm(output, { recursive: true, force: true });
  await mkdir(output, { recursive: true });
  const manifest = { ...identity, assets: {} };
  // One frontend build goes into both archives. No recordings, cache, .env,
  // node_modules, source checkout or build tools ever enter the device payload.
  const architectures =
    source && process.env.DASH_RELEASE_ARCH
      ? [process.env.DASH_RELEASE_ARCH]
      : ['arm64', 'amd64'];
  if (architectures.some((arch) => !['arm64', 'amd64'].includes(arch)))
    throw new Error('Unsupported build architecture');
  for (const arch of architectures) {
    const stage = resolve(`.artifacts/release-stage-${arch}`);
    await rm(stage, { recursive: true, force: true });
    await mkdir(stage, { recursive: true });
    execFileSync(
      'go',
      [
        'build',
        '-trimpath',
        '-ldflags',
        `-s -w -X main.buildVersion=${identity.version} -X main.buildChannel=${channel} -X main.buildRevision=${revision} -X main.buildSequence=${sequence}`,
        '-o',
        join(stage, 'dash-backend'),
        '.',
      ],
      {
        cwd: 'dash-backend',
        stdio: 'inherit',
        env: { ...process.env, CGO_ENABLED: '0', GOOS: 'linux', GOARCH: arch },
      },
    );
    await cp('dist', join(stage, 'dist'), { recursive: true });
    await cp(await buildMedia(arch), join(stage, 'bin'), { recursive: true });
    await writeFile(
      join(stage, 'release.json'),
      JSON.stringify({ ...identity, arch }) + '\n',
    );
    const name = `dash-${identity.tag.replaceAll('+', '-')}-linux-${arch}.tar.gz`;
    execFileSync(
      'tar',
      [
        '-czf',
        join(output, name),
        '-C',
        stage,
        'dash-backend',
        'release.json',
        'dist',
        'bin',
      ],
      { stdio: 'inherit', env: { ...process.env, COPYFILE_DISABLE: '1' } },
    );
    const data = await readFile(join(output, name));
    if (data.length > 128 * 1024 * 1024)
      throw new Error('Release archive exceeds the updater limit.');
    manifest.assets[arch] = {
      name,
      bytes: data.length,
      sha256: createHash('sha256').update(data).digest('hex'),
    };
    await rm(stage, { recursive: true, force: true });
  }
  await writeFile(
    join(output, 'manifest.json'),
    JSON.stringify(manifest, null, 2) + '\n',
  );
  await writeFile(
    join(output, 'SHA256SUMS'),
    Object.values(manifest.assets)
      .map((asset) => `${asset.sha256}  ${asset.name}`)
      .join('\n') + '\n',
  );
  console.log(
    `Packaged ${identity.tag}: ${(await readdir(output)).join(', ')}`,
  );
}
if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
)
  await main();
