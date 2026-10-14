import { describe, expect, it } from 'vitest';
import { presetLabel, presetsFrom, previewUrl } from './presets';

describe('presets', () => {
  it('keeps well-formed items only', () => {
    expect(presetsFrom([{ name: 'a.jpg', width: 10, height: 5 }, { name: 1 }, null, { name: 'b.png', width: '1', height: 2 }]))
      .toEqual([{ name: 'a.jpg', width: 10, height: 5 }]);
    expect(presetsFrom({})).toEqual([]);
  });
  it('builds labels and addresses', () => {
    expect(presetLabel('b-office_view.jpg')).toBe('b office view');
    expect(previewUrl('a b.jpg')).toBe('/api/backgrounds/a%20b.jpg/preview');
  });
});
