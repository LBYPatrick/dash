import { describe, it, expect } from 'vitest';
import { execFileSync } from 'node:child_process';
import { retimeFixture } from './fixture-clock.mjs';

const template = execFileSync('bzip2', [
  '-dc',
  'dash-backend/internal/telemetry/testdata/qlog.bz2',
]);

describe('synthetic recording clocks', () => {
  it('gives adjacent parts distinct monotonic and UTC clocks without changing the template', () => {
    const original = Buffer.from(template);
    const start = Date.parse('2026-10-12T17:42:08Z');
    const first = retimeFixture(template, start, 0);
    const second = retimeFixture(template, start + 11000, 11);
    expect(template).toEqual(original);
    expect(first.length).toBe(second.length);
    let gps = 0;
    for (let offset = 0; offset < first.length;) {
      const root = offset + 16;
      expect(second.readBigUInt64LE(root) - first.readBigUInt64LE(root)).toBe(
        11000000000n,
      );
      if (first.readUInt16LE(root + 8) === 47) {
        const pointer = root + first.readUInt16LE(offset + 12) * 8;
        const payload = pointer + 8 + (first.readInt32LE(pointer) >> 2) * 8;
        expect(
          second.readBigUInt64LE(payload + 40) -
            first.readBigUInt64LE(payload + 40),
        ).toBe(11000n);
        if (gps++ === 0)
          expect(first.readBigUInt64LE(payload + 40)).toBe(BigInt(start));
      }
      offset += 8 + first.readUInt32LE(offset + 4) * 8;
    }
    expect(gps).toBe(12);
  });
  it('rejects a changed or truncated template rather than silently corrupting fixture clocks', () => {
    expect(() => retimeFixture(Buffer.from([1, 2, 3]), 0, 0)).toThrow();
    const changed = Buffer.from(template);
    changed.writeUInt32LE(1, 0);
    expect(() => retimeFixture(changed, 0, 0)).toThrow();
    expect(() => retimeFixture(template.subarray(0, 20), 0, 0)).toThrow();
  });
});
