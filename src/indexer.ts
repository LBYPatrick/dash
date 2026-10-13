import { ref } from 'vue';
import { type Drive, type Entry } from './library';
export interface Position {
  latitude: number;
  longitude: number;
  altitude?: number;
  speed?: number;
  time: number;
  monotonic?: number;
}
export interface Telemetry {
  status: string;
  reason?: string;
  duration?: number;
  startMonotonic?: number;
  engagement?: { time: number; enabled: boolean; active: boolean }[];
  distanceKm?: number;
  distanceSamples?: number;
  points?: Position[];
  speeds?: { time: number; speed: number; steeringAngle?: number }[];
  car?: string;
  version?: string;
}
export interface IndexedSegment {
  files: Entry[];
  thumbnail?: string;
  metadata?: Telemetry;
  fingerprint: string;
}
export interface BackendIndex {
  version: number;
  status: {
    running: boolean;
    completed: number;
    total: number;
    errors: number;
    driving?: string;
  };
  segments: Record<
    string,
    {
      files: { name: string; size: number; mtime: number; duration?: number }[];
      thumbnail?: string;
      metadata: Telemetry;
    }
  >;
}
export const indexEntries = ref<Record<string, IndexedSegment>>({});
export const indexStatus = ref({
  running: false,
  completed: 0,
  total: 0,
  errors: 0,
  driving: 'unknown',
});
export const backendOrigin = ref('');
let controller: AbortController | undefined;
export async function detectBackend(
  address: string,
  signal?: AbortSignal,
): Promise<string> {
  const origin = new URL(address).origin;
  try {
    const response = await fetch(`${origin}/api/info`, {
      signal: signal
        ? AbortSignal.any([signal, AbortSignal.timeout(2500)])
        : AbortSignal.timeout(2500),
      credentials: 'omit',
    });
    const info = await response.json();
    return info.kind === 'dash-backend' && info.version === 1 ? origin : '';
  } catch {
    return '';
  }
}
export function stopIndexing() {
  controller?.abort();
  indexStatus.value.running = false;
}
export async function startIndexing(
  address: string,
  drives: Drive[],
  rebuild = false,
) {
  stopIndexing();
  const run = new AbortController();
  controller = run;
  indexEntries.value = {};
  const segments = drives.flatMap((drive) => drive.segments);
  indexStatus.value = {
    running: true,
    completed: 0,
    total: segments.length,
    errors: 0,
    driving: 'unknown',
  };
  backendOrigin.value = await detectBackend(address, run.signal);
  if (run.signal.aborted) return;
  if (backendOrigin.value) {
    const origin = backendOrigin.value;
    if (rebuild)
      await fetch(`${origin}/api/index/rebuild`, {
        method: 'POST',
        signal: run.signal,
      });
    do {
      try {
        const response = await fetch(`${origin}/api/index`, {
          signal: run.signal,
          cache: 'no-store',
        });
        if (!response.ok) throw new Error('Index unavailable');
        const snapshot: BackendIndex = await response.json();
        if (snapshot.version !== 1 || !snapshot.segments)
          throw new Error('Invalid backend index');
        const entries: Record<string, IndexedSegment> = {};
        for (const segment of segments) {
          const name = decodeURIComponent(
            new URL(segment.url).pathname.split('/').filter(Boolean).at(-1)!,
          );
          const indexed = snapshot.segments[name];
          if (!indexed) continue;
          entries[segment.url] = {
            fingerprint: '',
            metadata: {
              ...indexed.metadata,
              points: indexEntries.value[segment.url]?.metadata?.points,
              speeds: indexEntries.value[segment.url]?.metadata?.speeds,
              engagement: indexEntries.value[segment.url]?.metadata?.engagement,
            },
            thumbnail: indexed.thumbnail
              ? new URL(indexed.thumbnail, origin).href
              : undefined,
            files: indexed.files.map((file) => ({
              name: file.name,
              size: file.size,
              duration: file.duration,
              modified: file.mtime / 1e6,
              url: new URL(encodeURIComponent(file.name), segment.url).href,
            })),
          };
        }
        indexEntries.value = entries;
        indexStatus.value = {
          ...snapshot.status,
          driving: snapshot.status.driving || 'unknown',
        };
      } catch {
        if (!run.signal.aborted) indexStatus.value.errors++;
      }
      await new Promise<void>((resolve) => {
        const abort = () => {
          clearTimeout(timer);
          resolve();
        };
        const timer = setTimeout(() => {
          run.signal.removeEventListener('abort', abort);
          resolve();
        }, 2500);
        run.signal.addEventListener('abort', abort, { once: true });
      });
    } while (!run.signal.aborted);
    return;
  }
  indexStatus.value.running = false;
  indexStatus.value.errors++;
}
export function videoAddress(file: Entry, h264 = false): string | undefined {
  if (!backendOrigin.value || !/\.(hevc|ts)$/.test(file.name)) return undefined;
  const source = new URL(file.url);
  if (
    source.origin !== backendOrigin.value ||
    !source.pathname.startsWith('/files/')
  )
    return undefined;
  return `${backendOrigin.value}/api/video?${new URLSearchParams({ file: decodeURIComponent(source.pathname.slice(7)), codec: h264 ? 'h264' : 'copy' })}`;
}
export function distanceKm(points: Position[]): number {
  let distance = 0;
  for (let i = 1; i < points.length; i++) {
    const a = points[i - 1],
      b = points[i],
      dt = b.time - a.time;
    if (dt <= 0 || dt > 10) continue;
    const radians = Math.PI / 180;
    const h =
      Math.sin(((b.latitude - a.latitude) * radians) / 2) ** 2 +
      Math.cos(a.latitude * radians) *
        Math.cos(b.latitude * radians) *
        Math.sin(((b.longitude - a.longitude) * radians) / 2) ** 2;
    const km =
      6371 * 2 * Math.atan2(Math.sqrt(h), Math.sqrt(Math.max(0, 1 - h)));
    if (km / dt <= 0.09) distance += km;
  }
  return distance;
}

export function recordingAddress(drive: string, camera: string): string {
  return `/api/playlist?${new URLSearchParams({ drive, camera })}`;
}
