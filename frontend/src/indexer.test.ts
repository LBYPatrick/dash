import { describe, expect, it } from 'vite-plus/test';
import { recordingReadiness, type IndexedSegment } from './indexer';
import type { Drive } from './library';

const drive: Drive = {
  id: 'test--drive',
  date: 0,
  dated: false,
  segments: [{ index: 0, url: 'http://device/files/test--drive--0/' }],
};
function entry(): IndexedSegment {
  return {
    fingerprint: 'fixture',
    mediaVersion: 1,
    files: [
      {
        name: 'fcamera.hevc',
        size: 100,
        modified: 0,
        duration: 8,
        url: '/files/camera',
      },
    ],
  };
}
describe('native recording readiness', () => {
  it('keeps HEVC-only footage pending until its parked inventory exists', () => {
    const indexed = entry();
    const entries = { [drive.segments[0].url]: indexed };
    expect(recordingReadiness(drive, entries)).toMatchObject({
      ready: false,
      complete: 0,
      empty: false,
    });
    indexed.nativeVersion = 1;
    indexed.files[0].native = { status: 'ready', codec: 'hvc1.1.6.L150.B0' };
    expect(recordingReadiness(drive, entries)).toMatchObject({
      ready: true,
      complete: 1,
      empty: false,
    });
    indexed.files[0].native = { status: 'unsupported' };
    expect(recordingReadiness(drive, entries)).toMatchObject({
      ready: false,
      complete: 1,
      empty: true,
    });
  });
  it('keeps the road preview available while high-resolution indexing waits', () => {
    const indexed = entry();
    indexed.files.push({
      name: 'qcamera.ts',
      size: 100,
      modified: 0,
      duration: 8,
      url: '/files/road',
    });
    expect(
      recordingReadiness(drive, { [drive.segments[0].url]: indexed }),
    ).toMatchObject({ ready: true, complete: 1 });
  });
});
