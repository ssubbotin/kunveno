import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { prefersTcp, rememberTcp, transportFromStats, watchConnection } from './transport';

class MemoryStorage {
  private m = new Map<string, string>();
  getItem(k: string) { return this.m.get(k) ?? null; }
  setItem(k: string, v: string) { this.m.set(k, v); }
}

describe('TCP preference', () => {
  it('is off by default and on for a day after a failure', () => {
    const s = new MemoryStorage();
    const now = 1_000_000;
    expect(prefersTcp(s, now)).toBe(false);
    rememberTcp(s, now);
    expect(prefersTcp(s, now + 23 * 3600_000)).toBe(true);
    expect(prefersTcp(s, now + 25 * 3600_000)).toBe(false);
  });

  it('ignores garbage in storage', () => {
    const s = new MemoryStorage();
    s.setItem('kunveno.tcpUntil', 'soon');
    expect(prefersTcp(s, 0)).toBe(false);
  });
});

describe('watchConnection', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('reports failure after staying disconnected for the grace period', () => {
    const onFailed = vi.fn();
    const w = watchConnection(onFailed, 5000);
    w('connected');
    w('disconnected');
    vi.advanceTimersByTime(4999);
    expect(onFailed).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(onFailed).toHaveBeenCalledTimes(1);
  });

  it('does nothing when the connection recovers in time', () => {
    const onFailed = vi.fn();
    const w = watchConnection(onFailed, 5000);
    w('disconnected');
    vi.advanceTimersByTime(3000);
    w('connected');
    vi.advanceTimersByTime(10_000);
    expect(onFailed).not.toHaveBeenCalled();
  });

  it('reports failed at once, and only once', () => {
    const onFailed = vi.fn();
    const w = watchConnection(onFailed, 5000);
    w('disconnected');
    w('failed');
    vi.advanceTimersByTime(10_000);
    expect(onFailed).toHaveBeenCalledTimes(1);
  });
});

describe('transportFromStats', () => {
  it('follows the selected pair of the transport (Chrome)', () => {
    const stats = [
      { id: 'T1', type: 'transport', selectedCandidatePairId: 'P2' },
      { id: 'P1', type: 'candidate-pair', localCandidateId: 'L1', state: 'failed' },
      { id: 'P2', type: 'candidate-pair', localCandidateId: 'L2', state: 'succeeded', nominated: true },
      { id: 'L1', type: 'local-candidate', protocol: 'udp' },
      { id: 'L2', type: 'local-candidate', protocol: 'tcp' },
    ];
    expect(transportFromStats(stats)).toBe('tcp');
  });

  it('falls back to the selected candidate pair (Firefox)', () => {
    const stats = [
      { id: 'P1', type: 'candidate-pair', localCandidateId: 'L1', state: 'succeeded', selected: true },
      { id: 'L1', type: 'local-candidate', protocol: 'udp' },
    ];
    expect(transportFromStats(stats)).toBe('udp');
  });

  it('is empty before a pair is chosen', () => {
    expect(transportFromStats([{ id: 'P1', type: 'candidate-pair', localCandidateId: 'L1', state: 'in-progress' }])).toBe('');
  });
});
