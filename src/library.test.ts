import { describe, expect, it } from 'vitest';
import {
  groupDrives,
  parseListing,
  formatBytes,
  recordingDate,
} from './library';
import { distanceKm } from './indexer';
describe('recording inventory', () => {
  it('groups sparse timestamp and boot-id segments numerically', () => {
    const base = 'http://device:8088/files/';
    const listing = parseListing(
      {
        dirs: [
          { href: '2026-10-12--17-42-08--10/', ts: 100 },
          { href: '2026-10-12--17-42-08--2/', ts: 100 },
          { href: '000001ab--abcdef1234--0/', ts: 200 },
          { href: 'not-a-segment/' },
        ],
        files: [],
      },
      base,
    );
    const drives = groupDrives(listing.directories);
    expect(drives).toHaveLength(2);
    expect(drives[0].segments.map((segment) => segment.index)).toEqual([2, 10]);
    expect(drives[1].dated).toBe(false);
  });
  it('dates counter-ID routes from recorded UTC after copying the folder', () => {
    const drive = {
      id: '000001a3--c20ba54385',
      date: Date.now(),
      dated: false,
      segments: [{ index: 0, url: 'http://device/files/part0/' }],
    };
    const resolved = recordingDate(drive, {
      'http://device/files/part0/': { metadata: { startUTC: 1791826928 } },
    });
    expect(resolved.date).toBe(1791826928000);
    expect(resolved.dated).toBe(true);
    expect(recordingDate(drive, {})).toBe(drive);
  });
  it('rejects malformed listings and links escaping the root', () => {
    expect(() => parseListing('<html>', 'http://device/files/')).toThrow();
    const listing = parseListing(
      {
        dirs: [
          { href: '../secret/' },
          { href: 'https://other/recording/' },
          { href: 'safe/' },
        ],
        files: [],
      },
      'http://device/files/',
    );
    expect(listing.directories.map((entry) => entry.name)).toEqual(['safe']);
  });
  it('does not count GPS gaps or impossible jumps', () => {
    expect(
      distanceKm([
        { latitude: 0, longitude: 0, time: 1 },
        { latitude: 80, longitude: 100, time: 2 },
      ]),
    ).toBe(0);
    expect(formatBytes(1250000)).toBe('1.3 MB');
  });
});
