import { execFileSync, spawnSync } from 'node:child_process';
import { readFile } from 'node:fs/promises';
import { isDeepStrictEqual } from 'node:util';
const manifest = JSON.parse(
  await readFile('.artifacts/release/manifest.json', 'utf8'),
);
if (
  !['main', 'nightly', 'dev'].includes(manifest.channel) ||
  process.env.GITHUB_REF_NAME !== manifest.channel ||
  process.env.GITHUB_SHA !== manifest.revision
)
  throw new Error(
    'Publishing requires a channel commit and matching build manifest.',
  );
const gh = (...args) => execFileSync('gh', args, { stdio: 'inherit' });
const inspect = (tag) => {
  const result = spawnSync(
    'gh',
    ['release', 'view', tag, '--json', 'isDraft,assets'],
    { encoding: 'utf8' },
  );
  if (result.status === 0) return JSON.parse(result.stdout);
  if (/not found|HTTP 404/i.test(result.stderr)) return undefined;
  throw new Error(`Could not inspect release ${tag}: ${result.stderr}`);
};
const readManifest = (tag) =>
  JSON.parse(
    execFileSync(
      'gh',
      [
        'release',
        'download',
        tag,
        '--pattern',
        'manifest.json',
        '--output',
        '-',
      ],
      { encoding: 'utf8' },
    ),
  );
const feed = `channel-${manifest.channel}`;
const existingFeed = inspect(feed);
const previous = existingFeed?.assets.some(
  (asset) => asset.name === 'manifest.json',
)
  ? readManifest(feed)
  : undefined;
const advance = !previous || previous.sequence <= manifest.sequence;
const names = [
  'manifest.json',
  'SHA256SUMS',
  ...Object.values(manifest.assets).map((a) => a.name),
];
const existing = inspect(manifest.tag);
if (existing && !existing.isDraft) {
  // A feed upload can fail after an immutable build is published. Retry that
  // final step without rewriting any published bytes or making an old build
  // GitHub's "latest" release again.
  if (
    !isDeepStrictEqual(readManifest(manifest.tag), manifest) ||
    !names.every((name) => existing.assets.some((asset) => asset.name === name))
  )
    throw new Error(
      'Published release differs from this build; publish a new build number.',
    );
} else {
  if (!existing)
    gh(
      'release',
      'create',
      manifest.tag,
      '--target',
      manifest.revision,
      '--draft',
      '--title',
      `Dash ${manifest.version}`,
      '--notes',
      `Channel: ${manifest.channel}\nRevision: ${manifest.revision}\nCI build: ${manifest.sequence}\n\nNative Go backend and prebuilt PWA. Install over SSH or use Settings → Updates on an installed device.`,
      ...(manifest.channel === 'main' ? [] : ['--prerelease']),
    );
  gh(
    'release',
    'upload',
    manifest.tag,
    ...names.map((name) => `.artifacts/release/${name}`),
    '--clobber',
  );
  gh(
    'release',
    'edit',
    manifest.tag,
    '--draft=false',
    `--latest=${advance && manifest.channel === 'main'}`,
  );
}
if (!advance) {
  console.log(
    'A newer channel build is already available; retaining its feed.',
  );
} else {
  // Fixed feed tags locate manifests; they never identify the source being
  // installed. Only the immutable build tag inside the manifest does that.
  if (!existingFeed)
    gh(
      'release',
      'create',
      feed,
      '--target',
      manifest.revision,
      '--prerelease',
      '--latest=false',
      '--title',
      `Dash ${manifest.channel} update feed`,
      '--notes',
      'Rolling OTA manifest only. Its tag is a fixed feed locator; use the immutable build tag recorded in manifest.json for source and downloads.',
    );
  gh(
    'release',
    'upload',
    feed,
    '.artifacts/release/manifest.json',
    '--clobber',
  );
}
