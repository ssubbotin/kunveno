import { describe, expect, it } from 'vitest';
import { DEFAULT_REACTIONS, infoFrom } from './site.svelte';

describe('infoFrom', () => {
  it('takes the configured title and reactions', () => {
    expect(infoFrom({ title: 'Конференц-связь kunveno. Частный сервер.', reactions: ['👍', '☕'] }))
      .toEqual({ title: 'Конференц-связь kunveno. Частный сервер.', reactions: ['👍', '☕'] });
  });
  it('falls back to the defaults', () => {
    expect(infoFrom({})).toEqual({ title: 'kunveno', reactions: DEFAULT_REACTIONS });
    expect(infoFrom({ title: '   ', reactions: [] })).toEqual({ title: 'kunveno', reactions: DEFAULT_REACTIONS });
    expect(infoFrom(null)).toEqual({ title: 'kunveno', reactions: DEFAULT_REACTIONS });
  });
  it('ignores malformed reactions', () => {
    expect(infoFrom({ reactions: ['👍', 42, '', 'x'.repeat(40)] }).reactions).toEqual(['👍']);
  });
  it('defaults include thumbs down and coffee', () => {
    expect(DEFAULT_REACTIONS).toContain('👎');
    expect(DEFAULT_REACTIONS).toContain('☕');
  });
});
