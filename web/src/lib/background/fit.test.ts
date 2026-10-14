import { describe, expect, it } from 'vitest';
import { cover } from './fit';

describe('cover', () => {
  it('crops a wider image at the sides', () => {
    const { sx, sy } = cover(3200, 900, 1600, 900); // twice as wide as the frame
    expect(sx).toBeCloseTo(0.5);
    expect(sy).toBe(1);
  });
  it('crops a taller image at top and bottom', () => {
    const { sx, sy } = cover(1000, 1000, 1600, 900);
    expect(sx).toBe(1);
    expect(sy).toBeCloseTo(0.5625);
  });
  it('leaves an image of the same shape as it is', () => {
    expect(cover(1920, 1080, 640, 360)).toEqual({ sx: 1, sy: 1 });
  });
});
