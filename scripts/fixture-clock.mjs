// Retime only our checked, single-segment synthetic Cap'n Proto template.
// This is fixture tooling, not a decoder for device recordings.
export function retimeFixture(template, utcStart, monotonicOffset) {
  const data = Buffer.from(template);
  let firstUTC;
  for (let offset = 0; offset < data.length;) {
    if (offset + 8 > data.length || data.readUInt32LE(offset) !== 0)
      throw new Error(
        'Fixture must contain single-segment Cap’n Proto messages',
      );
    const start = offset + 8;
    const end = start + data.readUInt32LE(offset + 4) * 8;
    function structAt(pointer, minSize) {
      if (
        pointer < start ||
        pointer + 8 > end ||
        (data.readUInt32LE(pointer) & 3) !== 0
      )
        throw new Error('Unexpected fixture pointer');
      const target = pointer + 8 + (data.readInt32LE(pointer) >> 2) * 8;
      const words = data.readUInt16LE(pointer + 4);
      if (
        words * 8 < minSize ||
        target < start ||
        target + words * 8 > end ||
        end > data.length
      )
        throw new Error('Unexpected fixture struct');
      return { target, words };
    }
    const root = structAt(start, 10);
    const kind = data.readUInt16LE(root.target + 8);
    data.writeBigUInt64LE(
      data.readBigUInt64LE(root.target) + BigInt(monotonicOffset) * 1000000000n,
      root.target,
    );
    if (kind === 20 || kind === 47) {
      const payload = structAt(root.target + root.words * 8, 48);
      const timestamp = data.readBigUInt64LE(payload.target + 40);
      firstUTC ??= timestamp;
      data.writeBigUInt64LE(
        timestamp + BigInt(utcStart) - firstUTC,
        payload.target + 40,
      );
    }
    offset = end;
  }
  if (firstUTC === undefined) throw new Error('Fixture has no GPS clock');
  return data;
}
