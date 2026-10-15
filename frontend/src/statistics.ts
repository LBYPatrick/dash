import { t, locale, formatNumber } from './i18n';
import type { Drive } from './library';
import type { IndexedSegment } from './indexer';
export type ActivityMetric = 'distance' | 'duration' | 'drives';
export type ActivityGroup = 'drive' | 'day' | 'month';
export type ActivityPeriod = 7 | 30 | 90 | 'all';
export type DistanceUnit = 'km' | 'mi';
export interface DriveStatistics {
  drive: Drive;
  distance: number;
  duration: number;
  covered: number;
  distanceParts: number;
  durationParts: number;
  total: number;
}
export interface ActivityBucket {
  key: string;
  label: string;
  shortLabel: string;
  start: number;
  drives: DriveStatistics[];
}
const nonnegative = (n: unknown): n is number =>
  typeof n === 'number' && Number.isFinite(n) && n >= 0;
export function summarizeDrives(
  drives: Drive[],
  entries: Record<string, IndexedSegment>,
): DriveStatistics[] {
  return drives.map((drive) => {
    const result: DriveStatistics = {
      drive,
      distance: 0,
      duration: 0,
      covered: 0,
      distanceParts: 0,
      durationParts: 0,
      total: drive.segments.length,
    };
    for (const part of drive.segments) {
      const meta = entries[part.url]?.metadata;
      if (meta?.status !== 'ready') continue;
      result.covered++;
      if (nonnegative(meta.duration)) {
        result.duration += meta.duration;
        result.durationParts++;
      }
      // Old positive GPS totals remain usable. Old zeroes need parked reindexing
      // to distinguish a stationary recording from one without valid GPS pairs.
      const hasGPS =
        meta.distanceSamples === undefined
          ? (meta.distanceKm ?? 0) > 0
          : meta.distanceSamples > 0;
      if (hasGPS && nonnegative(meta.distanceKm)) {
        result.distance += meta.distanceKm;
        result.distanceParts++;
      }
    }
    return result;
  });
}
export function periodDrives(
  drives: DriveStatistics[],
  period: ActivityPeriod,
  now = Date.now(),
) {
  if (period === 'all') return drives;
  const start = new Date(now),
    end = new Date(now);
  start.setHours(0, 0, 0, 0);
  start.setDate(start.getDate() - period + 1);
  end.setHours(0, 0, 0, 0);
  end.setDate(end.getDate() + 1);
  return drives.filter(
    ({ drive }) => drive.date >= +start && drive.date < +end,
  );
}
export function validDate(date: number) {
  return (
    Number.isFinite(date) &&
    date >= 946684800000 &&
    Number.isFinite(new Date(date).getTime())
  );
}
export function activityBuckets(
  drives: DriveStatistics[],
  group: ActivityGroup,
): ActivityBucket[] {
  const buckets = new Map<string, ActivityBucket>();
  for (const item of drives) {
    if (!validDate(item.drive.date)) continue;
    const date = new Date(item.drive.date);
    const day = `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`;
    const key =
      group === 'drive'
        ? item.drive.id
        : group === 'month'
          ? day.slice(0, 7)
          : day;
    const dateOptions: Intl.DateTimeFormatOptions =
      group === 'month'
        ? { month: 'long', year: 'numeric' }
        : { month: 'short', day: 'numeric', year: 'numeric' };
    const time = date.toLocaleTimeString(locale.value, {
      hour: 'numeric',
      minute: '2-digit',
    });
    const label =
      date.toLocaleDateString(locale.value, dateOptions) +
      (group === 'drive' ? ` · ${time}` : '');
    const shortLabel =
      group === 'drive'
        ? time
        : date.toLocaleDateString(
            locale.value,
            group === 'month'
              ? { month: 'short', year: '2-digit' }
              : { month: 'short', day: 'numeric' },
          );
    const bucket = buckets.get(key) || {
      key,
      label,
      shortLabel,
      start: item.drive.date,
      drives: [],
    };
    bucket.start = Math.min(bucket.start, item.drive.date);
    bucket.drives.push(item);
    buckets.set(key, bucket);
  }
  return [...buckets.values()].sort(
    (a, b) => a.start - b.start || a.key.localeCompare(b.key),
  );
}
export function activityTotals(drives: DriveStatistics[]) {
  return drives.reduce(
    (sum, d) => ({
      distance: sum.distance + d.distance,
      duration: sum.duration + d.duration,
      covered: sum.covered + d.covered,
      total: sum.total + d.total,
      distanceParts: sum.distanceParts + d.distanceParts,
      durationParts: sum.durationParts + d.durationParts,
    }),
    {
      distance: 0,
      duration: 0,
      covered: 0,
      total: 0,
      distanceParts: 0,
      durationParts: 0,
    },
  );
}
export function metricValue(
  drives: DriveStatistics[],
  metric: ActivityMetric,
  unit: DistanceUnit,
) {
  if (metric === 'drives')
    return {
      value: drives.length,
      observed: drives.length,
      expected: drives.length,
    };
  const totals = activityTotals(drives);
  return metric === 'distance'
    ? {
        value: totals.distance * (unit === 'mi' ? 0.621371192237334 : 1),
        observed: totals.distanceParts,
        expected: totals.total,
      }
    : {
        value: totals.duration / 60,
        observed: totals.durationParts,
        expected: totals.total,
      };
}
export const formatStatistic = (value: number) =>
  formatNumber(value, {
    maximumFractionDigits: value > 0 && value < 0.01 ? 4 : 2,
  });
export const metricUnit = (metric: ActivityMetric, unit: DistanceUnit) =>
  metric === 'distance'
    ? t(`units.${unit}`)
    : metric === 'duration'
      ? t('facts.min')
      : t('statistics.drivesUnit');
export function describeMetric(
  drives: DriveStatistics[],
  metric: ActivityMetric,
  unit: DistanceUnit,
) {
  const { value, observed } = metricValue(drives, metric, unit);
  return observed
    ? t('statistics.value', {
        value: formatStatistic(value),
        unit: metricUnit(metric, unit),
      })
    : t('facts.notRecorded');
}
