import { describe, expect, it } from 'vitest';
import { forgetPin, knownRooms, pinFor, rememberPin } from './pins';

class MemoryStorage {
  private m = new Map<string, string>();
  getItem(k: string) { return this.m.get(k) ?? null; }
  setItem(k: string, v: string) { this.m.set(k, v); }
}

describe('room PINs', () => {
  it('keeps one PIN per room', () => {
    const s = new MemoryStorage();
    rememberPin(s, 'testing', '391722');
    rememberPin(s, 'tv', '692138');
    expect(pinFor(s, 'testing')).toBe('391722');
    expect(pinFor(s, 'tv')).toBe('692138');
    expect(pinFor(s, 'other')).toBe('');
    expect(knownRooms(s).sort()).toEqual(['testing', 'tv']);
  });

  it('forgets one room without touching the others', () => {
    const s = new MemoryStorage();
    rememberPin(s, 'testing', '391722');
    rememberPin(s, 'tv', '692138');
    forgetPin(s, 'tv');
    expect(pinFor(s, 'tv')).toBe('');
    expect(pinFor(s, 'testing')).toBe('391722');
  });

  it('survives garbage in storage', () => {
    const s = new MemoryStorage();
    s.setItem('kunveno.pins', 'not json');
    expect(pinFor(s, 'tv')).toBe('');
    rememberPin(s, 'tv', '692138');
    expect(pinFor(s, 'tv')).toBe('692138');
    s.setItem('kunveno.pins', '{"tv": 692138}');
    expect(pinFor(s, 'tv')).toBe('');
  });
});
