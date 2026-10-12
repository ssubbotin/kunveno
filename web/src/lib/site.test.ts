import { describe, expect, it } from 'vitest';
import { titleFrom } from './site';

describe('titleFrom', () => {
  it('takes the configured title', () => {
    expect(titleFrom({ title: 'Конференц-связь kunveno. Частный сервер.' })).toBe('Конференц-связь kunveno. Частный сервер.');
  });
  it('falls back to the product name', () => {
    expect(titleFrom({})).toBe('kunveno');
    expect(titleFrom({ title: '   ' })).toBe('kunveno');
    expect(titleFrom(null)).toBe('kunveno');
  });
});
