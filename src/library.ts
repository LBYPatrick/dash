export interface Entry {
  name: string;
  url: string;
  size: number;
  modified: number;
  duration?: number;
}
export interface Listing {
  directories: Entry[];
  files: Entry[];
}
export interface Segment {
  index: number;
  url: string;
}
export interface Drive {
  id: string;
  date: number;
  dated: boolean;
  segments: Segment[];
}

export function normalizeAddress(input: string): string {
  const url = new URL(input.includes('://') ? input : `http://${input}`);
  if (
    !['http:', 'https:'].includes(url.protocol) ||
    url.username ||
    url.password
  ) {
    throw new Error(
      'Use an HTTP or HTTPS folder address without a username or password.',
    );
  }
  url.hash = '';
  url.search = '';
  if (!url.pathname.endsWith('/')) url.pathname += '/';
  return url.href;
}

export function parseListing(data: unknown, base: string): Listing {
  if (
    !data ||
    typeof data !== 'object' ||
    !('dirs' in data) ||
    !('files' in data) ||
    !Array.isArray(data.dirs) ||
    !Array.isArray(data.files)
  ) {
    throw new Error(
      'This address did not return a Dash recordings folder. Check that dash-backend is running.',
    );
  }
  function entries(items: unknown[]): Entry[] {
    return items.flatMap((item) => {
      if (
        !item ||
        typeof item !== 'object' ||
        !('href' in item) ||
        typeof item.href !== 'string'
      )
        return [];
      const url = new URL(item.href, base);
      const root = new URL(base);
      if (
        url.origin !== root.origin ||
        !url.pathname.startsWith(root.pathname) ||
        url.pathname === root.pathname
      )
        return [];
      let name: string;
      try {
        name = decodeURIComponent(
          url.pathname.replace(/\/$/, '').split('/').pop() || '',
        );
      } catch {
        return [];
      }
      return [
        {
          name,
          url: url.href,
          size: 'sz' in item && typeof item.sz === 'number' ? item.sz : 0,
          modified:
            'ts' in item && typeof item.ts === 'number' ? item.ts * 1000 : 0,
        },
      ];
    });
  }
  return { directories: entries(data.dirs), files: entries(data.files) };
}

export async function listFolder(
  address: string,
  signal?: AbortSignal,
): Promise<Listing> {
  const url = new URL(address);
  url.searchParams.set('ls', '');
  const timeout = AbortSignal.timeout(15000);
  let response: Response;
  try {
    response = await fetch(url, {
      signal: signal ? AbortSignal.any([signal, timeout]) : timeout,
      credentials: 'omit',
      cache: 'no-store',
    });
  } catch (error) {
    if (signal?.aborted) throw error;
    throw new Error(
      'The device could not be reached. Join the device’s hotspot or local network, then reconnect.',
    );
  }
  if (!response.ok)
    throw new Error(
      response.status === 401 || response.status === 403
        ? 'This folder needs public read access. Dash connects without a login; check the backend configuration.'
        : `The device returned HTTP ${response.status}. Check the folder address and try again.`,
    );
  let data: unknown;
  try {
    data = await response.json();
  } catch {
    throw new Error(
      'Expected a Dash recordings folder listing. Check that the address points to your recordings folder.',
    );
  }
  return parseListing(data, address);
}

export function groupDrives(directories: Entry[]): Drive[] {
  const drives = new Map<string, Drive>();
  for (const entry of directories) {
    // Both timestamp routes and newer boot-id routes end with --<segment>.
    const match = entry.name.match(/^(.+--.+)--(\d+)$/);
    if (!match) continue;
    const id = match[1];
    const dateMatch = id.match(
      /^(\d{4}-\d{2}-\d{2})--(\d{2})-(\d{2})-(\d{2})$/,
    );
    const parsed = dateMatch
      ? new Date(
          `${dateMatch[1]}T${dateMatch[2]}:${dateMatch[3]}:${dateMatch[4]}`,
        ).getTime()
      : NaN;
    const dated = Number.isFinite(parsed);
    const drive = drives.get(id) || {
      id,
      date: dated ? parsed : entry.modified,
      dated,
      segments: [],
    };
    drive.segments.push({ index: Number(match[2]), url: entry.url });
    if (!dated) drive.date = Math.min(drive.date, entry.modified);
    drives.set(id, drive);
  }
  return [...drives.values()]
    .map((drive) => ({
      ...drive,
      segments: drive.segments.sort((a, b) => a.index - b.index),
    }))
    .sort((a, b) => b.date - a.date);
}

export function formatBytes(size: number): string {
  if (size < 1000) return `${size} B`;
  const unit = Math.min(Math.floor(Math.log(size) / Math.log(1000)), 3);
  return `${(size / 1000 ** unit).toFixed(1)} ${['B', 'KB', 'MB', 'GB'][unit]}`;
}

export function downloadUrl(address: string): string {
  const url = new URL(address, location.href);
  url.searchParams.set('dl', '');
  return url.href;
}
