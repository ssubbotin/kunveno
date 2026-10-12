import { describe, expect, it } from 'vitest';
import { initials } from './initials';

describe('initials', () => {
  it('takes the first letters of the first two words', () => {
    expect(initials('Сергей Субботин')).toBe('СС');
    expect(initials('  тёма ')).toBe('Т');
    expect(initials('Эш Кетчум из Олобастии')).toBe('ЭК');
  });
  it('falls back for an empty name', () => {
    expect(initials('')).toBe('?');
  });
});
