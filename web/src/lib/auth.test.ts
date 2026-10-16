import { afterEach, describe, expect, it, vi } from 'vitest';
import { INVITE_KEY, SIGN_IN_NOTICES, fetchMe, fixedName, guestFrom, restoreHash, signInURL, takeSignInNotice } from './auth';
import { messages } from './i18n/messages';

function memory(): Storage {
  const m = new Map<string, string>();
  return { getItem: (k) => m.get(k) ?? null, setItem: (k, v) => void m.set(k, v), removeItem: (k) => void m.delete(k),
    clear: () => m.clear(), key: (i) => [...m.keys()][i] ?? null, get length() { return m.size; } };
}

describe('auth', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('keeps the room link fragment across the sign-in redirect', () => {
    const s = memory();
    const url = signInURL('yandex', { pathname: '/join/ops', search: '', hash: '#pin=1234' } as Location, s);
    expect(url).toBe('/auth/yandex/start?return=%2Fjoin%2Fops');
    const back = { pathname: '/join/ops', search: '', hash: '' } as Location;
    let replaced = '';
    restoreHash(back, s, (u) => { replaced = u; });
    expect(replaced).toBe('/join/ops#pin=1234');
    restoreHash(back, s, (u) => { replaced = 'again ' + u; });
    expect(replaced).toBe('/join/ops#pin=1234'); // only once
  });

  it('keeps the query in the return path and forgets an older fragment', () => {
    const s = memory();
    signInURL('yandex', { pathname: '/join/ops', search: '', hash: '#pin=1' } as Location, s);
    expect(signInURL('yandex', { pathname: '/', search: '?a=1', hash: '' } as Location, s)).toBe('/auth/yandex/start?return=%2F%3Fa%3D1');
    let replaced = '';
    restoreHash({ pathname: '/', search: '?a=1', hash: '' } as Location, s, (u) => { replaced = u; });
    expect(replaced).toBe('');
  });

  it('never overwrites a fragment the page already has', () => {
    const s = memory();
    signInURL('yandex', { pathname: '/join/ops', search: '', hash: '#pin=1' } as Location, s);
    let replaced = '';
    restoreHash({ pathname: '/join/ops', search: '', hash: '#pin=2' } as Location, s, (u) => { replaced = u; });
    expect(replaced).toBe('');
    restoreHash({ pathname: '/join/ops', search: '', hash: '' } as Location, s, (u) => { replaced = u; });
    expect(replaced).toBe(''); // forgotten all the same
  });

  it('reads the signed-in person, or null without a session or a server', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ name: 'Анна', login: 'anna@example.com', provider: 'yandex' }), { status: 200 })));
    expect(await fetchMe()).toEqual({ name: 'Анна', login: 'anna@example.com', provider: 'yandex' });
    vi.stubGlobal('fetch', vi.fn(async () => new Response('{"error":"auth_required"}', { status: 401 })));
    expect(await fetchMe()).toBeNull();
    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('offline'); }));
    expect(await fetchMe()).toBeNull();
    vi.stubGlobal('fetch', vi.fn(async () => new Response('{"name":5}', { status: 200 })));
    expect(await fetchMe()).toBeNull();
  });

  it('takes the account name for the join, at most 32 characters, the login when the name is empty', () => {
    expect(fixedName({ name: '  Анна Петрова ', login: 'anna', provider: 'yandex' })).toBe('Анна Петрова');
    expect(fixedName({ name: 'Я'.repeat(40), login: 'anna', provider: 'yandex' })).toBe('Я'.repeat(32));
    expect(fixedName({ name: ' ', login: 'anna@example.com', provider: 'yandex' })).toBe('anna@example.com');
  });

  it('keeps a guest invitation for this tab and finds it again after a reload', () => {
    const s = memory();
    expect(guestFrom({ room: 'ops', pin: '', invite: 'tok' }, s)).toEqual({ room: 'ops', invite: 'tok' });
    expect(s.getItem(INVITE_KEY)).toBe('tok');
    expect(s.getItem('kunveno.room')).toBe('ops');
    // The address bar no longer carries the token; the tab still has it.
    expect(guestFrom({ room: 'ops', pin: '', invite: '' }, s)).toEqual({ room: 'ops', invite: 'tok' });
    // Another room, a PIN link or another page is no guest visit.
    expect(guestFrom({ room: 'dev', pin: '', invite: '' }, s)).toBeNull();
    expect(guestFrom({ room: 'ops', pin: '1234', invite: '' }, s)).toBeNull();
    expect(guestFrom(null, s)).toBeNull();
  });
});

describe('fetchMe at start-up', () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });
  it('gives up on a server that does not answer', async () => {
    vi.useFakeTimers();
    vi.stubGlobal('fetch', vi.fn((_url: string, init?: RequestInit) => new Promise((_res, rej) => {
      init?.signal?.addEventListener('abort', () => rej(new DOMException('aborted', 'AbortError')));
    })));
    let me: unknown = 'pending';
    void fetchMe().then((v) => (me = v));
    await vi.advanceTimersByTimeAsync(3000);
    expect(me).toBeNull();
  });
});

describe('the notice of a failed sign-in', () => {
  function take(search: string, pathname = '/', hash = '') {
    let replaced: string | null = null;
    const notice = takeSignInNotice({ pathname, search, hash } as Location, (u) => { replaced = u; });
    return { notice, replaced };
  }

  it('reads the reason the server sent back and clears it from the address bar', () => {
    expect(take('?signin=refused')).toEqual({ notice: 'refused', replaced: '/' });
    expect(take('?signin=cancelled', '/join/ops', '#pin=1234')).toEqual({ notice: 'cancelled', replaced: '/join/ops#pin=1234' });
    expect(take('?x=1&signin=unavailable')).toEqual({ notice: 'unavailable', replaced: '/?x=1' });
    expect(take('?signin=expired')).toEqual({ notice: 'expired', replaced: '/' });
  });

  it('ignores an unknown reason and leaves an address without one alone', () => {
    expect(take('?signin=<b>hi</b>')).toEqual({ notice: '', replaced: '/' });
    expect(take('')).toEqual({ notice: '', replaced: null });
    expect(take('?x=1')).toEqual({ notice: '', replaced: null });
  });

  it('has a text for every reason in every language', () => {
    for (const lang of ['ru', 'en', 'es'] as const) {
      for (const key of Object.values(SIGN_IN_NOTICES)) {
        expect(messages[lang][key], `${lang} ${key}`).toBeTruthy();
      }
    }
  });
});
