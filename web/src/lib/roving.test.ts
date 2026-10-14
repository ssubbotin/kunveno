import { describe, expect, it } from 'vitest';
import { nextIndex } from './roving';

describe('nextIndex', () => {
  it('moves forward and back with wraparound', () => {
    expect(nextIndex(0, 3, 'ArrowRight')).toBe(1);
    expect(nextIndex(2, 3, 'ArrowDown')).toBe(0);
    expect(nextIndex(0, 3, 'ArrowLeft')).toBe(2);
    expect(nextIndex(1, 3, 'ArrowUp')).toBe(0);
  });

  it('jumps to the ends', () => {
    expect(nextIndex(1, 3, 'Home')).toBe(0);
    expect(nextIndex(1, 3, 'End')).toBe(2);
  });

  it('starts from the ends when focus is outside', () => {
    expect(nextIndex(-1, 3, 'ArrowDown')).toBe(0);
    expect(nextIndex(-1, 3, 'ArrowUp')).toBe(2);
  });

  it('ignores other keys and empty lists', () => {
    expect(nextIndex(0, 3, 'Tab')).toBeNull();
    expect(nextIndex(0, 3, 'a')).toBeNull();
    expect(nextIndex(-1, 0, 'ArrowDown')).toBeNull();
  });
});
