import { execFileSync, spawnSync } from 'node:child_process';
import { mkdir, copyFile, rm } from 'node:fs/promises';
import { resolve, relative, dirname, join } from 'node:path';
// Seed only this project's public, checksum-verified module archives. Never
// send the user's full Go cache, environment, credentials, or private modules.
let cacheContext;
try {
  execFileSync('go', ['version'], { stdio: 'ignore' });
  const downloadRoot = join(
    execFileSync('go', ['env', 'GOMODCACHE'], { encoding: 'utf8' }).trim(),
    'cache/download',
  );
  const output = execFileSync('go', ['mod', 'download', '-json', 'all'], {
    cwd: 'dash-backend',
    encoding: 'utf8',
    maxBuffer: 8 * 1024 * 1024,
  });
  const modules = output
    .trim()
    .split(/\n(?=\{)/)
    .map((item) => JSON.parse(item));
  const destination = resolve('.artifacts/docker-go-cache');
  await rm(destination, { recursive: true, force: true });
  await mkdir(destination, { recursive: true });
  for (const module of modules) {
    if (module.Error) throw new Error(module.Error);
    for (const source of [
      module.Info,
      module.GoMod,
      module.Zip,
      module.Zip && `${module.Zip}hash`,
    ].filter(Boolean)) {
      const path = relative(downloadRoot, source);
      if (path.startsWith('..'))
        throw new Error('Module outside download cache');
      const target = join(destination, path);
      await mkdir(dirname(target), { recursive: true });
      await copyFile(source, target);
    }
  }
  cacheContext = destination;
  console.log(
    `Seeded ${modules.length} project module archives for the Linux build.`,
  );
} catch (error) {
  console.log(
    `Local Go cache unavailable; Docker will download dependencies normally (${error.message}).`,
  );
}
const result = spawnSync(
  'docker',
  ['compose', 'up', '--build', '-d', '--wait'],
  {
    stdio: 'inherit',
    env: {
      ...process.env,
      ...(cacheContext ? { DASH_GO_CACHE_CONTEXT: cacheContext } : {}),
    },
  },
);
process.exit(result.status ?? 1);
