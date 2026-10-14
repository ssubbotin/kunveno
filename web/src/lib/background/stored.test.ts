import { afterEach, describe, expect, it, vi } from 'vitest';

const own = vi.hoisted(() => ({ has: false as boolean | null }));
vi.mock('./ownImage', () => ({ hasOwn: async () => own.has }));

import { isLost } from './stored';

const list = (...names: string[]) => async () => ({ ok: true, json: async () => names.map((name) => ({ name, width: 1920, height: 1080 })) });

describe('isLost', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('never loses none or blur', async () => {
    vi.stubGlobal('fetch', list());
    expect(await isLost({ kind: 'none' })).toBe(false);
    expect(await isLost({ kind: 'blur' })).toBe(false);
  });
  it('loses a server image only when the list says it is gone', async () => {
    vi.stubGlobal('fetch', list('beach.jpg'));
    expect(await isLost({ kind: 'image', source: 'server', name: 'beach.jpg' })).toBe(false);
    expect(await isLost({ kind: 'image', source: 'server', name: 'gone.jpg' })).toBe(true);
    vi.stubGlobal('fetch', async () => ({ ok: false, status: 500, json: async () => [] }));
    expect(await isLost({ kind: 'image', source: 'server', name: 'gone.jpg' })).toBe(false);
  });
  it('loses the own image only when this browser says it is deleted', async () => {
    own.has = true;
    expect(await isLost({ kind: 'image', source: 'own' })).toBe(false);
    own.has = false;
    expect(await isLost({ kind: 'image', source: 'own' })).toBe(true);
    own.has = null; // storage could not be read
    expect(await isLost({ kind: 'image', source: 'own' })).toBe(false);
  });
});
