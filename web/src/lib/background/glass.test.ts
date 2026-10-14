import { describe, expect, it } from 'vitest';
import { blockShift } from './glass';

describe('blockShift', () => {
  it('stays within a third of a block and is the same every time', () => {
    for (let bx = 0; bx < 16; bx++) {
      for (let by = 0; by < 9; by++) {
        const [x, y] = blockShift(bx, by);
        expect(Math.abs(x)).toBeLessThanOrEqual(0.3);
        expect(Math.abs(y)).toBeLessThanOrEqual(0.3);
        expect(blockShift(bx, by)).toEqual([x, y]);
      }
    }
  });
  it('differs between neighbouring blocks', () => {
    const shifts = new Set<string>();
    for (let bx = 0; bx < 16; bx++) for (let by = 0; by < 9; by++) shifts.add(blockShift(bx, by).map((v) => v.toFixed(3)).join(','));
    expect(shifts.size).toBeGreaterThan(140);
  });
});
