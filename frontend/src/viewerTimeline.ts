import { t } from './i18n';
import type { Position, Telemetry } from './indexer';
export interface TimelineSegment {
  start: number;
  duration: number;
  thumbnail?: string;
  metadata?: Telemetry;
}
export interface ViewerPosition extends Position {
  offset: number;
}
export function formatTimestamp(seconds: number, precision: 0 | 3 = 0): string {
  const safe = Number.isFinite(seconds) ? Math.max(0, seconds) : 0;
  const milliseconds = Math.round(safe * 1000);
  const total = precision ? Math.floor(milliseconds / 1000) : Math.floor(safe);
  const fraction = precision
    ? `.${String(milliseconds % 1000).padStart(3, '0')}`
    : '';
  const s = `${String(total % 60).padStart(2, '0')}${fraction}`;
  const minutes = Math.floor(total / 60);
  return minutes >= 60
    ? `${Math.floor(minutes / 60)}:${String(minutes % 60).padStart(2, '0')}:${s}`
    : `${minutes}:${s}`;
}
export function parseTimestamp(value: string): number | undefined {
  const normalized = value.trim().replace(',', '.');
  if (!/^\d+(?::[0-5]?\d){0,2}(?:\.\d{1,3})?$/.test(normalized)) return;
  const result = normalized
    .split(':')
    .reduce((sum, part) => sum * 60 + Number(part), 0);
  return Number.isFinite(result) ? result : undefined;
}
export function timelinePositions(
  segments: TimelineSegment[],
): ViewerPosition[] {
  return segments
    .flatMap((segment) => {
      const meta = segment.metadata;
      const points = meta?.points || [];
      const anchor = meta?.startMonotonic ?? points[0]?.monotonic;
      if (anchor === undefined) return [];
      return points
        .filter((point) => point.monotonic !== undefined)
        .map((point) => ({
          ...point,
          offset: segment.start + point.monotonic! - anchor,
        }))
        .filter(
          (point) =>
            point.offset >= segment.start &&
            point.offset <= segment.start + segment.duration,
        );
    })
    .sort((a, b) => a.offset - b.offset);
}
export function positionAt(
  points: ViewerPosition[],
  offset: number,
): Position | undefined {
  if (
    !points.length ||
    offset < points[0].offset ||
    offset > points.at(-1)!.offset
  )
    return;
  let low = 0,
    high = points.length - 1;
  while (low < high) {
    const middle = Math.ceil((low + high) / 2);
    if (points[middle].offset <= offset) low = middle;
    else high = middle - 1;
  }
  const a = points[low],
    b = points[low + 1];
  // Never interpolate across a missing GPS interval.
  if (!b || b.offset - a.offset > 3 || b.offset === a.offset)
    return offset - a.offset <= 2 ? a : undefined;
  const fraction = (offset - a.offset) / (b.offset - a.offset);
  return {
    ...a,
    latitude: a.latitude + (b.latitude - a.latitude) * fraction,
    longitude: a.longitude + (b.longitude - a.longitude) * fraction,
  };
}
export function engagementAt(
  segments: TimelineSegment[],
  offset: number,
  includeConfidence = false,
): string {
  const segment = segments.find(
    (part) => offset >= part.start && offset < part.start + part.duration,
  );
  if (!segment) return t('player.engagementNotRecorded');
  const meta = segment.metadata,
    samples = meta?.engagement || [];
  const anchor =
    meta?.startMonotonic ?? meta?.points?.[0]?.monotonic ?? samples[0]?.time;
  if (anchor === undefined) return t('player.engagementNotRecorded');
  const time = offset - segment.start + anchor;
  let sample: (typeof samples)[number] | undefined;
  for (let i = samples.length - 1; i >= 0; i--) {
    if (samples[i].time <= time) {
      sample = samples[i];
      break;
    }
  }
  if (!sample || time - sample.time > 3)
    return t('player.engagementNotRecorded');
  const confidence =
    includeConfidence &&
    sample.active &&
    sample.confidence &&
    ['high', 'medium', 'low'].includes(sample.confidence)
      ? t(`engagement.confidence_${sample.confidence}`)
      : '';
  return sample.active
    ? [t('player.engaged'), confidence].filter(Boolean).join(' · ')
    : sample.enabled
      ? t('engagement.enabledInactive')
      : t('engagement.disengaged');
}
export function engagementBands(segments: TimelineSegment[]) {
  const bands: {
    start: number;
    end: number;
    state: string;
    confidence?: 'high' | 'medium' | 'low';
  }[] = [];
  for (const part of segments) {
    const meta = part.metadata;
    const samples = meta?.engagement || [];
    const anchor =
      meta?.startMonotonic ?? meta?.points?.[0]?.monotonic ?? samples[0]?.time;
    if (anchor === undefined) continue;
    for (let i = 0; i < samples.length; i++) {
      const sample = samples[i],
        next = samples[i + 1];
      const start = Math.max(part.start, part.start + sample.time - anchor);
      const end = Math.min(
        part.start + part.duration,
        part.start +
          Math.min(next?.time ?? sample.time + 3, sample.time + 3) -
          anchor,
      );
      if (end <= start) continue;
      const state = sample.active
        ? 'engaged'
        : sample.enabled
          ? 'overriding'
          : 'disengaged';
      const confidence =
        sample.active &&
        sample.confidence &&
        ['high', 'medium', 'low'].includes(sample.confidence)
          ? sample.confidence
          : undefined;
      const previous = bands.at(-1);
      if (
        previous?.state === state &&
        previous.confidence === confidence &&
        Math.abs(previous.end - start) < 0.01
      )
        previous.end = end;
      else bands.push({ start, end, state, confidence });
    }
  }
  // A bounded visual overview; the status at the exact playhead remains precise.
  if (bands.length <= 256) return bands;
  const stride = Math.ceil(bands.length / 256);
  return bands.filter((_, i) => i % stride === 0).slice(0, 256);
}
