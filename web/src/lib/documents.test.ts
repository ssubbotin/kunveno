import { describe, expect, it } from 'vitest';
import { deleteDocument, documentErrorText, documentFileName, fetchDocument } from './documents';

function fakeFetch(res: Response | Error) {
  const calls: { url: string; init: RequestInit }[] = [];
  const fn = async (url: string, init: RequestInit) => {
    calls.push({ url, init });
    if (res instanceof Error) throw res;
    return res;
  };
  return { fn, calls };
}

describe('fetchDocument', () => {
  it('posts the PIN for one meeting\'s document and returns the file', async () => {
    const name = 'ops-2026-11-14-1030-Протокол.md';
    const f = fakeFetch(new Response('# Протокол', {
      status: 200,
      headers: { 'Content-Disposition': `attachment; filename="ops-2026-11-14-1030-minutes.md"; filename*=UTF-8''${encodeURIComponent(name)}` },
    }));
    const r = await fetchDocument(f.fn, 'ops', '1234', 'minutes', 1792137300123);
    expect(f.calls[0].url).toBe('/api/rooms/ops/meetings/1792137300123/documents/minutes');
    expect(f.calls[0].init.method).toBe('POST');
    expect(JSON.parse(String(f.calls[0].init.body))).toEqual({ pin: '1234' });
    if (!r.ok) throw new Error(r.error);
    expect(r.filename).toBe(name);
    expect(await r.blob.text()).toBe('# Протокол');
  });
  it('names each failure', async () => {
    expect(await fetchDocument(fakeFetch(new Response('', { status: 403 })).fn, 'ops', 'x', 'a', 1)).toEqual({ ok: false, error: 'bad_pin' });
    expect(await fetchDocument(fakeFetch(new Response('', { status: 404 })).fn, 'ops', 'x', 'a', 1)).toEqual({ ok: false, error: 'gone' });
    expect(await fetchDocument(fakeFetch(new Response('', { status: 429, headers: { 'Retry-After': '30' } })).fn, 'ops', 'x', 'a', 1))
      .toEqual({ ok: false, error: 'too_many', retryAfter: 30 });
    expect(await fetchDocument(fakeFetch(new Error('offline')).fn, 'ops', 'x', 'a', 1)).toEqual({ ok: false, error: 'failed' });
  });
});

describe('deleteDocument', () => {
  it('deletes one meeting\'s document with the PIN', async () => {
    const f = fakeFetch(new Response(null, { status: 204 }));
    expect(await deleteDocument(f.fn, 'ops', '1234', 'plan', 5)).toEqual({ ok: true });
    expect(f.calls[0].url).toBe('/api/rooms/ops/meetings/5/documents/plan');
    expect(f.calls[0].init.method).toBe('DELETE');
    expect(JSON.parse(String(f.calls[0].init.body))).toEqual({ pin: '1234' });
    expect(await deleteDocument(fakeFetch(new Response('', { status: 404 })).fn, 'ops', '1', 'plan', 5)).toEqual({ ok: false, error: 'gone' });
  });
});

describe('documentFileName', () => {
  it('prefers the UTF-8 name, then the plain one, then the id', () => {
    expect(documentFileName(`attachment; filename="a.md"; filename*=UTF-8''%D0%BF%D0%BB%D0%B0%D0%BD.md`, 'ops', 'plan')).toBe('план.md');
    expect(documentFileName('attachment; filename="ops-2026-11-14-1030-Plan.md"', 'ops', 'plan')).toBe('ops-2026-11-14-1030-Plan.md');
    expect(documentFileName(null, 'ops', 'plan')).toBe('ops-plan.md');
  });
  it('refuses names that leave the folder or hide', () => {
    for (const bad of ['..%2Fx.md', '.hidden.md', 'a%2Fb.md', 'a%5Cb.md', '%E0%A4%A']) {
      expect(documentFileName(`attachment; filename*=UTF-8''${bad}`, 'ops', 'plan')).toBe('ops-plan.md');
    }
  });
});

describe('documentErrorText', () => {
  const tr = (k: string, v?: Record<string, string | number>) => (v ? `${k}:${JSON.stringify(v)}` : k);
  it('words each failure', () => {
    expect(documentErrorText({ error: 'bad_pin' }, tr)).toBe('notes.badPin');
    expect(documentErrorText({ error: 'gone' }, tr)).toBe('documents.gone');
    expect(documentErrorText({ error: 'too_many', retryAfter: 9 }, tr)).toBe('pin.tooMany:{"seconds":9}');
    expect(documentErrorText({ error: 'failed' }, tr)).toBe('documents.failed');
    expect(documentErrorText({ error: 'failed' }, tr, 'documents.deleteFailed')).toBe('documents.deleteFailed');
  });
});
