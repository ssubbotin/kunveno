import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Reconnector } from './reconnect';

describe('Reconnector', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('keeps at most one pending retry however often it is asked', () => {
    const run = vi.fn();
    const r = new Reconnector(run);
    r.schedule();
    r.schedule(); // the same failure reported twice (error and close)
    r.schedule();
    vi.advanceTimersByTime(60_000);
    expect(run).toHaveBeenCalledTimes(1);
  });

  it('backs off 1, 2, 4 ... 10 s and starts over after a success', () => {
    const r = new Reconnector(() => {});
    const delays: number[] = [];
    for (let i = 0; i < 6; i++) {
      delays.push(r.schedule()!);
      vi.runAllTimers();
    }
    expect(delays).toEqual([1000, 2000, 4000, 8000, 10000, 10000]);
    r.succeeded();
    expect(r.schedule()).toBe(1000);
  });

  it('cancel drops the pending retry', () => {
    const run = vi.fn();
    const r = new Reconnector(run);
    r.schedule();
    r.cancel();
    vi.runAllTimers();
    expect(run).not.toHaveBeenCalled();
  });
});

describe('newClientId', () => {
  it('matches the server rule and differs per page', async () => {
    const { newClientId } = await import('./reconnect');
    const a = newClientId(), b = newClientId();
    expect(a).toMatch(/^[A-Za-z0-9_-]{8,64}$/);
    expect(a).not.toBe(b);
  });
});
