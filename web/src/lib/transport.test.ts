import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { forgetTcp, prefersTcp, rememberTcp, transportFromStats, watchConnection } from './transport';

class MemoryStorage {
  private m = new Map<string, string>();
  getItem(k: string) { return this.m.get(k) ?? null; }
  setItem(k: string, v: string) { this.m.set(k, v); }
  removeItem(k: string) { this.m.delete(k); }
}

describe('TCP preference', () => {
  it('is off by default and on for a week after a failure', () => {
    const s = new MemoryStorage();
    const now = 1_000_000;
    const day = 24 * 3600_000;
    expect(prefersTcp(s, now)).toBe(false);
    rememberTcp(s, now);
    expect(prefersTcp(s, now + 6 * day)).toBe(true);
    expect(prefersTcp(s, now + 8 * day)).toBe(false);
  });

  it('is off again once forgotten', () => {
    const s = new MemoryStorage();
    rememberTcp(s, 0);
    forgetTcp(s);
    expect(prefersTcp(s, 1)).toBe(false);
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
    expect(onFailed).toHaveBeenCalledWith('media_disconnected');
  });

  it('waits 15 s by default, so outages of a few seconds pass', () => {
    const onFailed = vi.fn();
    const w = watchConnection(onFailed);
    w('connected');
    w('disconnected');
    vi.advanceTimersByTime(14_999);
    expect(onFailed).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(onFailed).toHaveBeenCalledWith('media_disconnected');
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

  it('reports a connection that never comes up within the connect window', () => {
    const onFailed = vi.fn();
    const w = watchConnection(onFailed, 5000, 8000);
    w('new');
    w('connecting');
    vi.advanceTimersByTime(7999);
    expect(onFailed).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(onFailed).toHaveBeenCalledTimes(1);
    expect(onFailed).toHaveBeenCalledWith('connect_timeout');
  });

  it('a connection that comes up in time cancels the connect window', () => {
    const onFailed = vi.fn();
    const w = watchConnection(onFailed, 5000, 8000);
    w('new');
    vi.advanceTimersByTime(3000);
    w('connected');
    vi.advanceTimersByTime(20_000);
    expect(onFailed).not.toHaveBeenCalled();
  });

  it('reports failed at once, and only once', () => {
    const onFailed = vi.fn();
    const w = watchConnection(onFailed, 5000);
    w('disconnected');
    w('failed');
    vi.advanceTimersByTime(10_000);
    expect(onFailed).toHaveBeenCalledTimes(1);
    expect(onFailed).toHaveBeenCalledWith('media_failed');
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
