import { describe, expect, it } from 'vitest';
import { fetchSummary, summaryState } from './summary';

function fakeFetch(res: Response | Error) {
  const calls: { url: string; init: RequestInit }[] = [];
  const fn = async (url: string, init: RequestInit) => {
    calls.push({ url, init });
    if (res instanceof Error) throw res;
    return res;
  };
  return { fn, calls };
}

describe('summaryState', () => {
  it('reads the three states and nothing else', () => {
    expect(summaryState('ready')).toBe('ready');
    expect(summaryState('pending')).toBe('pending');
    expect(summaryState('failed')).toBe('failed');
    for (const v of ['x', '', undefined, null, 3, {}]) expect(summaryState(v)).toBeNull();
  });
});

describe('fetchSummary', () => {
  it('posts the PIN and the language and returns the file', async () => {
    const f = fakeFetch(new Response('# Итоги', { status: 200, headers: { 'Content-Disposition': 'attachment; filename="ops-2026-10-16-1055-summary.md"' } }));
    const r = await fetchSummary(f.fn, 'ops', '1234', 'ru');
    expect(f.calls[0].url).toBe('/api/rooms/ops/summary');
    expect(f.calls[0].init.method).toBe('POST');
    expect(JSON.parse(String(f.calls[0].init.body))).toEqual({ pin: '1234', lang: 'ru' });
    if (!r.ok) throw new Error(r.error);
    expect(r.filename).toBe('ops-2026-10-16-1055-summary.md');
    expect(await r.blob.text()).toBe('# Итоги');
  });
  it('falls back to a summary file name', async () => {
    const r = await fetchSummary(fakeFetch(new Response('x', { status: 200 })).fn, 'ops', '1234', 'ru');
    if (!r.ok) throw new Error(r.error);
    expect(r.filename).toBe('ops-summary.md');
  });
  it('names each failure', async () => {
    expect(await fetchSummary(fakeFetch(new Response('', { status: 403 })).fn, 'ops', 'x', 'ru')).toEqual({ ok: false, error: 'bad_pin' });
    expect(await fetchSummary(fakeFetch(new Response('', { status: 404 })).fn, 'ops', 'x', 'ru')).toEqual({ ok: false, error: 'gone' });
    expect(await fetchSummary(fakeFetch(new Response('', { status: 429, headers: { 'Retry-After': '42' } })).fn, 'ops', 'x', 'ru'))
      .toEqual({ ok: false, error: 'too_many', retryAfter: 42 });
    expect(await fetchSummary(fakeFetch(new Error('offline')).fn, 'ops', 'x', 'ru')).toEqual({ ok: false, error: 'failed' });
  });
});
