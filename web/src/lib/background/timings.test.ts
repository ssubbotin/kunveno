import { describe, expect, it } from 'vitest';
import { Timings } from './timings';

describe('Timings', () => {
  it('summarises each name and starts a new window', () => {
    const t = new Timings();
    for (const ms of [5, 1, 3, 2, 4]) t.add('infer', ms);
    t.add('draw', 0.26);
    expect(t.take()).toEqual({ infer: { n: 5, p50: 3, p95: 5, max: 5 }, draw: { n: 1, p50: 0.3, p95: 0.3, max: 0.3 } });
    expect(t.take()).toEqual({});
  });
});
