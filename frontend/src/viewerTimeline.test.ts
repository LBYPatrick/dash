import { describe, it, expect } from 'vite-plus/test';
import {
  parseTimestamp,
  formatTimestamp,
  timelinePositions,
  positionAt,
  engagementAt,
  engagementBands,
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
  it('accepts a decimal comma without accepting ambiguous mixed separators', () => {
    expect(parseTimestamp('1:02,125')).toBe(62.125);
    expect(parseTimestamp('12,5')).toBe(12.5);
    expect(parseTimestamp('1:02,1.25')).toBeUndefined();
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
  it('keeps unknown confidence distinct and preserves recorded confidence changes', () => {
    const parts: Parameters<typeof engagementAt>[0] = [
      {
        start: 0,
        duration: 6,
        metadata: {
          status: 'ready',
          startMonotonic: 100,
          engagement: [
            { time: 100, enabled: false, active: false },
            { time: 101, enabled: true, active: true },
            { time: 102, enabled: true, active: true, confidence: 'high' },
            { time: 103, enabled: true, active: true, confidence: 'medium' },
            { time: 104, enabled: true, active: true, confidence: 'low' },
            { time: 105, enabled: true, active: false },
          ],
        },
      },
    ];
    const bands = engagementBands(parts);
    expect(engagementAt(parts, 1, true)).toBe('Engaged');
    expect(engagementAt(parts, 2, true)).toBe('Engaged · High confidence');
    expect(engagementAt(parts, 3, true)).toBe('Engaged · Medium confidence');
    expect(engagementAt(parts, 4, true)).toBe('Engaged · Low confidence');
    expect(engagementAt(parts, 5, true)).toBe('Enabled · inactive');
    expect(engagementAt(parts, 9, true)).toBe('Engagement not recorded');
    expect(bands.map((b) => [b.state, b.confidence])).toEqual([
      ['disengaged', undefined],
      ['engaged', undefined],
      ['engaged', 'high'],
      ['engaged', 'medium'],
      ['engaged', 'low'],
      ['overriding', undefined],
    ]);
  });
});
