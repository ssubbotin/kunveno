import { afterEach, describe, expect, it, vi } from 'vitest';
import { fetchPresets, presetLabel, presetsFrom, previewUrl } from './presets';

describe('presets', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('keeps well-formed items only', () => {
    expect(presetsFrom([{ name: 'a.jpg', width: 10, height: 5 }, { name: 1 }, null, { name: 'b.png', width: '1', height: 2 }]))
      .toEqual([{ name: 'a.jpg', width: 10, height: 5 }]);
    expect(presetsFrom({})).toEqual([]);
  });
  it('builds labels and addresses', () => {
    expect(presetLabel('b-office_view.jpg')).toBe('b office view');
    expect(previewUrl('a b.jpg')).toBe('/api/backgrounds/a%20b.jpg/preview');
  });
  // A failed request is unknown, never an empty list: an empty list would mean every preset is gone.
  it('tells a failed request from an empty list', async () => {
    vi.stubGlobal('fetch', async () => ({ ok: true, json: async () => [] }));
    expect(await fetchPresets()).toEqual([]);
    vi.stubGlobal('fetch', async () => ({ ok: false, status: 502, json: async () => [] }));
    expect(await fetchPresets()).toBeNull();
    vi.stubGlobal('fetch', async () => { throw new TypeError('network'); });
    expect(await fetchPresets()).toBeNull();
    vi.stubGlobal('fetch', async () => ({ ok: true, json: async () => { throw new SyntaxError('html'); } }));
    expect(await fetchPresets()).toBeNull();
  });
});
