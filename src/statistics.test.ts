import { describe, it, expect } from 'vitest';
import {
  summarizeDrives,
  activityBuckets,
  activityTotals,
  periodDrives,
  metricValue,
} from './statistics';
import type { Drive } from './library';
import type { IndexedSegment } from './indexer';
const date = (day: number) => +new Date(2026, 9, day, 12);
const drives: Drive[] = [
  {
    id: 'one',
    date: date(12),
    dated: true,
    segments: [
      { index: 0, url: 'a' },
      { index: 1, url: 'b' },
    ],
  },
  {
    id: 'two',
    date: date(12),
    dated: true,
    segments: [{ index: 0, url: 'c' }],
  },
  {
    id: 'three',
    date: date(1),
    dated: false,
    segments: [{ index: 0, url: 'd' }],
  },
];
function entry(metadata: IndexedSegment['metadata']): IndexedSegment {
  return { files: [], fingerprint: '', metadata };
}
describe('retained recording statistics', () => {
  it('separates missing distance from measured zero and partial logs', () => {
    const rows = summarizeDrives(drives, {
      a: entry({
        status: 'ready',
        duration: 60,
        distanceKm: 2,
        distanceSamples: 4,
      }),
      b: entry({ status: 'error' }),
      c: entry({
        status: 'ready',
        duration: 0,
        distanceKm: 0,
        distanceSamples: 1,
      }),
      d: entry({
        status: 'ready',
        duration: 20,
        distanceKm: 0,
        distanceSamples: 0,
      }),
    });
    expect(activityTotals(rows)).toEqual({
      distance: 2,
      duration: 80,
      covered: 3,
      total: 4,
      distanceParts: 2,
      durationParts: 3,
    });
    expect(metricValue([rows[1]], 'distance', 'km')).toEqual({
      value: 0,
      observed: 1,
      expected: 1,
    });
    expect(metricValue([rows[2]], 'distance', 'km').observed).toBe(0);
    expect(metricValue(rows, 'drives', 'km').value).toBe(3);
    expect(metricValue(rows, 'distance', 'mi').value).toBeCloseTo(1.242742);
    const legacy = summarizeDrives(drives, {
      a: entry({ status: 'ready', distanceKm: 2 }),
      c: entry({ status: 'ready', distanceKm: 0 }),
    });
    expect(metricValue([legacy[0]], 'distance', 'km').observed).toBe(1);
    expect(metricValue([legacy[1]], 'distance', 'km').observed).toBe(0);
  });
  it('groups local dates and scopes calendar ranges without dropping unknown metadata', () => {
    const rows = summarizeDrives(drives, {});
    expect(activityBuckets(rows, 'day').map((b) => b.drives.length)).toEqual([
      1, 2,
    ]);
    expect(activityBuckets(rows, 'month')).toHaveLength(1);
    expect(activityBuckets(rows, 'drive')).toHaveLength(3);
    expect(periodDrives(rows, 7, date(13))).toHaveLength(2);
    expect(periodDrives(rows, 'all', date(13))).toHaveLength(3);
    expect(
      activityBuckets(
        [{ ...rows[0], drive: { ...drives[0], date: NaN } }],
        'day',
      ),
    ).toHaveLength(0);
  });
});
