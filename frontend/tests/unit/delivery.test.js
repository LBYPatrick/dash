import { it, expect } from 'vite-plus/test';
import { readFileSync } from 'node:fs';
import { parse } from 'yaml';
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
  expect(workflow.jobs.build.needs).toBe('media');
  expect(workflow.jobs.media.strategy.matrix.include).toEqual([
    { arch: 'arm64', runner: 'ubuntu-24.04-arm' },
    { arch: 'amd64', runner: 'ubuntu-24.04' },
  ]);
  expect(workflow.jobs.publish.permissions.contents).toBe('write');
  expect(workflow.permissions.contents).toBe('read');
});
