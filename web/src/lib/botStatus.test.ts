import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ANNOUNCE_GAP, Throttle, focusAfterHelp } from './botStatus';

describe('Throttle', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('passes the first value at once and the latest of a burst after the gap', () => {
    const out: string[] = [];
    const th = new Throttle<string>(ANNOUNCE_GAP, (v) => out.push(v));
    th.push('слушает');
    expect(out).toEqual(['слушает']);
    vi.advanceTimersByTime(500);
    th.push('думает');
    th.push('говорит');
    expect(out).toEqual(['слушает']);
    vi.advanceTimersByTime(ANNOUNCE_GAP - 501);
    expect(out).toEqual(['слушает']);
    vi.advanceTimersByTime(1);
    expect(out).toEqual(['слушает', 'говорит']);
    vi.advanceTimersByTime(ANNOUNCE_GAP);
    th.push('ждёт'); // a gap has passed: at once
    expect(out).toEqual(['слушает', 'говорит', 'ждёт']);
  });

  it('drops a waiting value on stop', () => {
    const out: string[] = [];
    const th = new Throttle<string>(ANNOUNCE_GAP, (v) => out.push(v));
    th.push('a');
    th.push('b');
    th.stop();
    vi.advanceTimersByTime(ANNOUNCE_GAP * 2);
    expect(out).toEqual(['a']);
  });
});

describe('focusAfterHelp', () => {
  const list = { isConnected: true, name: 'list' };
  it('returns to the button that opened the help while it is in the page', () => {
    const button = { isConnected: true, name: 'button' };
    expect(focusAfterHelp(button, list)).toBe(button);
  });
  it('falls back to the participant list once the button is gone with the bot', () => {
    expect(focusAfterHelp({ isConnected: false, name: 'gone' }, list)).toBe(list);
    expect(focusAfterHelp(null, list)).toBe(list);
  });
  it('gives nothing when neither is in the page', () => {
    expect(focusAfterHelp({ isConnected: false, name: 'a' }, { isConnected: false, name: 'b' })).toBeNull();
    expect(focusAfterHelp(null, null)).toBeNull();
  });
});
