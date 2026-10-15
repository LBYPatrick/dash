import { describe, it, expect } from 'vitest';
import { recordingReadiness, type IndexedSegment } from './indexer';
import type { Drive } from './library';
const drive: Drive = {
  id: 'test',
  date: 0,
  dated: false,
  segments: [
    { index: 0, url: 'p0' },
    { index: 1, url: 'p1' },
  ],
};
const part = (size = 100, duration = 60): IndexedSegment => ({
  fingerprint: '',
  mediaVersion: 1,
  files: [{ name: 'qcamera.ts', url: 'video', modified: 0, size, duration }],
});
describe('recording readiness', () => {
  it('keeps a partly indexed timeline locked and unlocks only complete footage', () => {
    expect(recordingReadiness(drive, { p0: part() }).ready).toBe(false);
    expect(recordingReadiness(drive, { p0: part(), p1: part() }).ready).toBe(
      true,
    );
  });
  it('allows missing metadata and empty final parts, but distinguishes no footage', () => {
    expect(
      recordingReadiness(drive, { p0: part(), p1: part(0, 0) }).ready,
    ).toBe(true);
    expect(
      recordingReadiness(drive, { p0: part(0, 0), p1: part(0, 0) }).empty,
    ).toBe(true);
    expect(
      recordingReadiness(drive, {
        p0: { ...part(), mediaVersion: 0 },
        p1: part(),
      }).ready,
    ).toBe(false);
  });
});
