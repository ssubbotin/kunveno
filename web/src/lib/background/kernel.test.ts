import { describe, expect, it } from 'vitest';
import { gaussian } from './kernel';

describe('gaussian', () => {
  it('is normalised and falls off from the centre', () => {
    const w = gaussian(6, 3);
    expect(w).toHaveLength(7);
    expect(w[0] + 2 * w.slice(1).reduce((a, b) => a + b, 0)).toBeCloseTo(1, 5);
    for (let i = 1; i < w.length; i++) expect(w[i]).toBeLessThan(w[i - 1]);
  });
});
