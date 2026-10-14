import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { parse } from 'yaml';
import { releaseIdentity, sourceIdentity } from './release.mjs';
const revision = 'a'.repeat(40);
describe('delivery channels', () => {
  it('runs CI only on channel commits, never feature branches, PRs or tags', () => {
    const workflow = parse(
      readFileSync('.github/workflows/channels.yml', 'utf8'),
    );
    expect(workflow.on).toEqual({
      push: { branches: ['main', 'nightly', 'dev'] },
    });
    expect(workflow.concurrency['cancel-in-progress']).toBe(false);
    expect(workflow.concurrency.queue).toBe('max');
    expect(workflow.jobs.publish.needs).toBe('build');
    expect(workflow.jobs.publish.permissions.contents).toBe('write');
    expect(workflow.permissions.contents).toBe('read');
  });
  it('gives each channel an immutable, unique build identity', () => {
    for (const channel of ['main', 'nightly', 'dev']) {
      const first = releaseIdentity(channel, '13', revision, '0.1.0');
      expect(first.tag).toBe(
        `v0.1.0${channel === 'main' ? '+' : '-'}${channel}.13.aaaaaaaa`,
      );
      expect(releaseIdentity(channel, '14', revision, '0.1.0').tag).not.toBe(
        first.tag,
      );
    }
  });
  it('marks source builds separately and records uncommitted changes', () => {
    expect(sourceIdentity('123', revision, '0.1.0', true)).toMatchObject({
      channel: 'local',
      tag: 'v0.1.0-local.123.aaaaaaaa.dirty',
    });
    expect(sourceIdentity('123', revision, '0.1.0', false).tag).toBe(
      'v0.1.0-local.123.aaaaaaaa',
    );
    expect(() => sourceIdentity('0', revision, '0.1.0', false)).toThrow();
  });
  it('refuses feature refs and malformed/unsafe release inputs', () => {
    for (const channel of ['feature/a', 'local', 'stable', ''])
      expect(() => releaseIdentity(channel, '1', revision, '0.1.0')).toThrow();
    for (const number of ['0', '-1', '1;echo no', '9007199254740992'])
      expect(() => releaseIdentity('dev', number, revision, '0.1.0')).toThrow();
    expect(() => releaseIdentity('dev', '1', 'unknown', '0.1.0')).toThrow();
    expect(() => releaseIdentity('dev', '1', revision, '0.1.0-dev')).toThrow();
  });
});

// Publishing retries run against a fake gh executable: these tests cannot
// create a release, contact GitHub, or use a real token.
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';
function publishFixture(sequence, previousSequence, mismatch = false) {
  const root = mkdtempSync(join(tmpdir(), 'dash-publish-'));
  const identity = releaseIdentity('dev', String(sequence), revision, '0.1.0');
  const manifest = {
    ...identity,
    assets: {
      arm64: {
        name: `dash-${identity.tag}-linux-arm64.tar.gz`,
        bytes: 1,
        sha256: 'a'.repeat(64),
      },
      amd64: {
        name: `dash-${identity.tag}-linux-amd64.tar.gz`,
        bytes: 1,
        sha256: 'b'.repeat(64),
      },
    },
  };
  mkdirSync(join(root, '.artifacts/release'), { recursive: true });
  writeFileSync(
    join(root, '.artifacts/release/manifest.json'),
    JSON.stringify(manifest),
  );
  writeFileSync(
    join(root, 'fixture.json'),
    JSON.stringify({ manifest, previousSequence, mismatch }),
  );
  writeFileSync(
    join(root, 'gh'),
    `#!/usr/bin/env node
const fs=require('fs');const root=process.env.DASH_PUBLISH_TEST_ROOT;const fixture=JSON.parse(fs.readFileSync(root+'/fixture.json'));const args=process.argv.slice(2);fs.appendFileSync(root+'/calls.jsonl',JSON.stringify(args)+'\\n');
const tag=args[2];if(args[1]==='view'){console.log(JSON.stringify({isDraft:false,assets:(tag.startsWith('channel-')?['manifest.json']:['manifest.json','SHA256SUMS',...Object.values(fixture.manifest.assets).map(a=>a.name)]).map(name=>({name}))}));}else if(args[1]==='download'){let data=structuredClone(fixture.manifest);if(tag.startsWith('channel-'))data.sequence=fixture.previousSequence;else if(fixture.mismatch)data.revision='b'.repeat(40);console.log(JSON.stringify(data));}else if(args[1]!=='upload'){console.error('Unexpected mutation: '+args.join(' '));process.exit(1);}
`,
    { mode: 0o700 },
  );
  const result = spawnSync(
    process.execPath,
    [resolve('scripts/publish-release.mjs')],
    {
      cwd: root,
      encoding: 'utf8',
      env: {
        ...process.env,
        PATH: `${root}:${process.env.PATH}`,
        DASH_PUBLISH_TEST_ROOT: root,
        GITHUB_REF_NAME: 'dev',
        GITHUB_SHA: revision,
        GH_TOKEN: '',
      },
    },
  );
  const calls = readFileSync(join(root, 'calls.jsonl'), 'utf8')
    .trim()
    .split('\n')
    .map((line) => JSON.parse(line));
  rmSync(root, { recursive: true, force: true });
  return { result, calls };
}
describe('immutable release publication recovery', () => {
  it('resumes a failed feed upload without rewriting a published build', () => {
    const { result, calls } = publishFixture(12, 11);
    expect(result.status, result.stderr).toBe(0);
    expect(
      calls.filter((args) => args[1] === 'upload').map((args) => args[2]),
    ).toEqual(['channel-dev']);
    expect(calls.some((args) => args[1] === 'edit')).toBe(false);
  });
  it('does not move a newer feed or GitHub latest backwards on retry', () => {
    const { result, calls } = publishFixture(12, 14);
    expect(result.status, result.stderr).toBe(0);
    expect(
      calls.some((args) => ['upload', 'edit', 'create'].includes(args[1])),
    ).toBe(false);
  });
  it('refuses to overwrite a published build with different metadata', () => {
    const { result, calls } = publishFixture(12, 11, true);
    expect(result.status).not.toBe(0);
    expect(
      calls.some((args) => ['upload', 'edit', 'create'].includes(args[1])),
    ).toBe(false);
  });
});
