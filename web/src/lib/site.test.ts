import { afterEach, describe, expect, it, vi } from 'vitest';
import { DEFAULT_REACTIONS, STARTUP_TIMEOUT_MS, infoFrom, loadInfo, requireSignIn, site } from './site.svelte';

const off = { enabled: false, providers: [] };

describe('infoFrom', () => {
  it('takes the configured title and reactions', () => {
    expect(infoFrom({ title: 'Конференц-связь kunveno. Частный сервер.', reactions: ['👍', '☕'] }))
      .toEqual({ title: 'Конференц-связь kunveno. Частный сервер.', reactions: ['👍', '☕'], noiseSuppression: false, notesEnabled: false, notesLive: false, recordingEnabled: false, facilitatorEnabled: false, reconnectGrace: 15, auth: { enabled: false, providers: [] } });
  });
  it('falls back to the defaults', () => {
    expect(infoFrom({})).toEqual({ title: 'kunveno', reactions: DEFAULT_REACTIONS, noiseSuppression: false, notesEnabled: false, notesLive: false, recordingEnabled: false, facilitatorEnabled: false, reconnectGrace: 15, auth: { enabled: false, providers: [] } });
    expect(infoFrom({ title: '   ', reactions: [] })).toEqual({ title: 'kunveno', reactions: DEFAULT_REACTIONS, noiseSuppression: false, notesEnabled: false, notesLive: false, recordingEnabled: false, facilitatorEnabled: false, reconnectGrace: 15, auth: { enabled: false, providers: [] } });
    expect(infoFrom(null)).toEqual({ title: 'kunveno', reactions: DEFAULT_REACTIONS, noiseSuppression: false, notesEnabled: false, notesLive: false, recordingEnabled: false, facilitatorEnabled: false, reconnectGrace: 15, auth: { enabled: false, providers: [] } });
  });
  it('ignores malformed reactions', () => {
    expect(infoFrom({ reactions: ['👍', 42, '', 'x'.repeat(40)] }).reactions).toEqual(['👍']);
  });
  it('defaults include thumbs down and coffee', () => {
    expect(DEFAULT_REACTIONS).toContain('👎');
    expect(DEFAULT_REACTIONS).toContain('☕');
  });
});

describe('infoFrom noise_suppression', () => {
  it('reads noise_suppression as a boolean only', () => {
    expect(infoFrom({ noise_suppression: true }).noiseSuppression).toBe(true);
    expect(infoFrom({ noise_suppression: 'true' }).noiseSuppression).toBe(false);
    expect(infoFrom({}).noiseSuppression).toBe(false);
  });
});

describe('infoFrom notes', () => {
  it('reads notes and notes_live as booleans only', () => {
    expect(infoFrom({ notes: true, notes_live: true })).toMatchObject({ notesEnabled: true, notesLive: true });
    expect(infoFrom({ notes: 'true', notes_live: 1 })).toMatchObject({ notesEnabled: false, notesLive: false, recordingEnabled: false, facilitatorEnabled: false, reconnectGrace: 15, auth: { enabled: false, providers: [] } });
    expect(infoFrom({ notes_live: true })).toMatchObject({ notesEnabled: false, notesLive: false, recordingEnabled: false, facilitatorEnabled: false, reconnectGrace: 15, auth: { enabled: false, providers: [] } });
  });
  it('reads the facilitator flag', () => {
    expect(infoFrom({ facilitator: true }).facilitatorEnabled).toBe(true);
    expect(infoFrom({}).facilitatorEnabled).toBe(false);
  });

  it('reads the recording flag', () => {
    expect(infoFrom({ recording: true }).recordingEnabled).toBe(true);
    expect(infoFrom({}).recordingEnabled).toBe(false);
  });
});

describe('infoFrom reconnect_grace', () => {
  it('takes whole seconds from 3 to 120 and falls back to 15', () => {
    expect(infoFrom({ reconnect_grace: 30 }).reconnectGrace).toBe(30);
    expect(infoFrom({ reconnect_grace: 3 }).reconnectGrace).toBe(3);
    expect(infoFrom({ reconnect_grace: 120 }).reconnectGrace).toBe(120);
    for (const bad of [2, 121, 0, -5, 7.5, '30', null, undefined, NaN]) {
      expect(infoFrom({ reconnect_grace: bad }).reconnectGrace).toBe(15);
    }
    expect(infoFrom({}).reconnectGrace).toBe(15);
  });
});

describe('infoFrom auth', () => {
  it('reads sign-in and its providers', () => {
    const info = infoFrom({ auth: { enabled: true, providers: [{ type: 'yandex', label: 'Yandex 360' }] } });
    expect(info.auth.enabled).toBe(true);
    expect(info.auth.providers).toEqual([{ type: 'yandex', label: 'Yandex 360' }]);
  });
  it('treats a missing or malformed block as sign-in off', () => {
    for (const auth of [undefined, null, 'on', true, [], { enabled: 'true', providers: [] }, { providers: [{ type: 'yandex', label: 'Y' }] }]) {
      expect(infoFrom({ auth }).auth, String(auth)).toEqual(off);
    }
  });
  it('keeps only well-formed providers, at most 8, with labels up to 40 characters', () => {
    const many = Array.from({ length: 12 }, (_, i) => ({ type: `p${i}`, label: `P${i}` }));
    expect(infoFrom({ auth: { enabled: true, providers: many } }).auth.providers).toHaveLength(8);
    const mixed = [{ type: 'yandex', label: 'Yandex 360' }, { type: 5, label: 'x' }, { type: 'a', label: 7 }, { type: '', label: 'x' },
      { type: 'b', label: 'x'.repeat(41) }, { type: 'c', label: 'x'.repeat(40) }, null, 'yandex'];
    expect(infoFrom({ auth: { enabled: true, providers: mixed } }).auth.providers).toEqual([{ type: 'yandex', label: 'Yandex 360' }, { type: 'c', label: 'x'.repeat(40) }]);
    expect(infoFrom({ auth: { enabled: true, providers: 'yandex' } }).auth).toEqual({ enabled: true, providers: [] });
  });
});

describe('loading the details', () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    site.auth = off;
    site.loaded = false;
  });

  // A server that never answers, but gives up when the request is aborted.
  function hanging() {
    vi.stubGlobal('fetch', vi.fn((_url: string, init?: RequestInit) => new Promise((_res, rej) => {
      init?.signal?.addEventListener('abort', () => rej(new DOMException('aborted', 'AbortError')));
    })));
  }
  const answer = (body: unknown, status = 200) => vi.fn(async () => new Response(JSON.stringify(body), { status }));

  it('gives up on a server that does not answer and keeps the defaults', async () => {
    vi.useFakeTimers();
    hanging();
    let done = false;
    void loadInfo().then(() => (done = true));
    await vi.advanceTimersByTimeAsync(STARTUP_TIMEOUT_MS - 1);
    expect(done).toBe(false);
    await vi.advanceTimersByTimeAsync(1);
    expect(done).toBe(true);
    expect(site.loaded).toBe(false);
    expect(site.auth).toEqual(off);
  });

  it('remembers that the details arrived', async () => {
    vi.stubGlobal('fetch', answer({ title: 'x', auth: { enabled: true, providers: [{ type: 'yandex', label: 'Yandex 360' }] } }));
    await loadInfo();
    expect(site.loaded).toBe(true);
    expect(site.auth.enabled).toBe(true);
  });

  it('a refusal for want of a session turns sign-in on, keeping known providers', async () => {
    const f = answer({});
    vi.stubGlobal('fetch', f);
    site.auth = { enabled: false, providers: [{ type: 'yandex', label: 'Yandex 360' }] };
    await requireSignIn();
    expect(site.auth).toEqual({ enabled: true, providers: [{ type: 'yandex', label: 'Yandex 360' }] });
    expect(f).not.toHaveBeenCalled();
  });

  it('fetches the providers again when none are known', async () => {
    vi.stubGlobal('fetch', answer({ auth: { enabled: true, providers: [{ type: 'yandex', label: 'Yandex 360' }] } }));
    await requireSignIn();
    expect(site.auth).toEqual({ enabled: true, providers: [{ type: 'yandex', label: 'Yandex 360' }] });
  });

  it('stays on with no providers when the details still cannot be read, or say sign-in is off', async () => {
    vi.stubGlobal('fetch', answer('bad gateway', 502));
    await requireSignIn();
    expect(site.auth).toEqual({ enabled: true, providers: [] });
    vi.stubGlobal('fetch', answer({ auth: { enabled: false } }));
    await requireSignIn();
    expect(site.auth).toEqual({ enabled: true, providers: [] });
  });
});
