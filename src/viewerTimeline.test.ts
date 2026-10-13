import { describe, it, expect } from 'vitest';
import {
  parseTimestamp,
  formatTimestamp,
  timelinePositions,
  positionAt,
  engagementAt,
} from './viewerTimeline';
describe('viewer time and telemetry', () => {
  it('accepts precise timestamps and rejects malformed minutes', () => {
    expect(parseTimestamp('1:02:03.5')).toBe(3723.5);
    expect(parseTimestamp('12.5')).toBe(12.5);
    expect(parseTimestamp('1:99')).toBeUndefined();
    expect(parseTimestamp('-2')).toBeUndefined();
    expect(formatTimestamp(3723)).toBe('1:02:03');
    expect(formatTimestamp(3.125, 3)).toBe('0:03.125');
    expect(formatTimestamp(59.9996, 3)).toBe('1:00.000');
    expect(formatTimestamp(3599.9996, 3)).toBe('1:00:00.000');
    expect(formatTimestamp(-1, 3)).toBe('0:00.000');
    expect(formatTimestamp(NaN, 3)).toBe('0:00.000');
    expect(parseTimestamp('1:02:03.125')).toBe(3723.125);
    expect(parseTimestamp('0:03.1234')).toBeUndefined();
  });
  it('aligns boot-relative samples to actual camera part durations', () => {
    const parts = [
      {
        start: 0,
        duration: 8,
        metadata: {
          status: 'ready',
          startMonotonic: 100,
          points: [
            { latitude: 1, longitude: 2, time: 1000, monotonic: 102 },
            { latitude: 3, longitude: 4, time: 1002, monotonic: 104 },
          ],
        },
      },
      {
        start: 8,
        duration: 8,
        metadata: {
          status: 'ready',
          startMonotonic: 200,
          points: [{ latitude: 10, longitude: 20, time: 2000, monotonic: 201 }],
        },
      },
    ];
    const points = timelinePositions(parts);
    expect(points.map((p) => p.offset)).toEqual([2, 4, 9]);
    expect(positionAt(points, 3)?.latitude).toBe(2);
    expect(positionAt(points, 0)).toBeUndefined();
    expect(positionAt(points, 8)).toBeUndefined();
    expect(positionAt(points, 9)?.latitude).toBe(10);
  });
  it('distinguishes recorded engagement from unavailable and stale data', () => {
    const parts = [
      {
        start: 0,
        duration: 10,
        metadata: {
          status: 'ready',
          startMonotonic: 100,
          engagement: [
            { time: 101, enabled: true, active: true },
            { time: 102, enabled: true, active: false },
            { time: 103, enabled: false, active: false },
          ],
        },
      },
    ];
    expect(engagementAt(parts, 0)).toBe('Engagement not recorded');
    expect(engagementAt(parts, 1)).toBe('Engaged');
    expect(engagementAt(parts, 2)).toBe('Enabled · inactive');
    expect(engagementAt(parts, 3)).toBe('Disengaged');
    expect(engagementAt(parts, 9)).toBe('Engagement not recorded');
  });
});
