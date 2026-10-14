import { describe, expect, it } from 'vitest';
import { deleteNotes, fetchNotes, fileName, notesErrorText, notesUntil } from './notes';

function fakeFetch(res: Response | Error) {
  const calls: { url: string; init: RequestInit }[] = [];
  const fn = async (url: string, init: RequestInit) => {
    calls.push({ url, init });
    if (res instanceof Error) throw res;
    return res;
  };
  return { fn, calls };
}

describe('fetchNotes', () => {
  it('posts the PIN and the language and returns the file', async () => {
    const f = fakeFetch(new Response('# Заметки', { status: 200, headers: { 'Content-Disposition': 'attachment; filename="ops-2026-10-14-0937.md"' } }));
    const r = await fetchNotes(f.fn, 'ops', '1234', 'en');
    expect(f.calls[0].url).toBe('/api/rooms/ops/notes');
    expect(f.calls[0].init.method).toBe('POST');
    expect(JSON.parse(String(f.calls[0].init.body))).toEqual({ pin: '1234', lang: 'en' });
    if (!r.ok) throw new Error(r.error);
    expect(r.filename).toBe('ops-2026-10-14-0937.md');
    expect(await r.blob.text()).toBe('# Заметки');
  });
  it('reports a failed body read as failed', async () => {
    const res = new Response('x', { status: 200 });
    res.blob = () => Promise.reject(new Error('reset'));
    expect(await fetchNotes(fakeFetch(res).fn, 'ops', '1234', 'ru')).toEqual({ ok: false, error: 'failed' });
  });
  it('names each failure', async () => {
    expect(await fetchNotes(fakeFetch(new Response('', { status: 403 })).fn, 'ops', 'x', 'ru')).toEqual({ ok: false, error: 'bad_pin' });
    expect(await fetchNotes(fakeFetch(new Response('', { status: 404 })).fn, 'ops', 'x', 'ru')).toEqual({ ok: false, error: 'gone' });
    expect(await fetchNotes(fakeFetch(new Response('', { status: 429, headers: { 'Retry-After': '42' } })).fn, 'ops', 'x', 'ru'))
      .toEqual({ ok: false, error: 'too_many', retryAfter: 42 });
    expect(await fetchNotes(fakeFetch(new Response('', { status: 500 })).fn, 'ops', 'x', 'ru')).toEqual({ ok: false, error: 'failed' });
    expect(await fetchNotes(fakeFetch(new Error('offline')).fn, 'ops', 'x', 'ru')).toEqual({ ok: false, error: 'failed' });
  });
});

describe('deleteNotes', () => {
  it('sends DELETE with the PIN and succeeds on 204', async () => {
    const f = fakeFetch(new Response(null, { status: 204 }));
    expect(await deleteNotes(f.fn, 'ops team', '1234')).toEqual({ ok: true });
    expect(f.calls[0].url).toBe('/api/rooms/ops%20team/notes');
    expect(f.calls[0].init.method).toBe('DELETE');
    expect(JSON.parse(String(f.calls[0].init.body))).toEqual({ pin: '1234' });
  });
  it('names each failure', async () => {
    const del = (res: Response | Error) => deleteNotes(fakeFetch(res).fn, 'ops', 'x');
    expect(await del(new Response('', { status: 403 }))).toEqual({ ok: false, error: 'bad_pin' });
    expect(await del(new Response('', { status: 404 }))).toEqual({ ok: false, error: 'gone' });
    expect(await del(new Response('', { status: 409 }))).toEqual({ ok: false, error: 'recording' });
    expect(await del(new Response('', { status: 429, headers: { 'Retry-After': '42' } }))).toEqual({ ok: false, error: 'too_many', retryAfter: 42 });
    expect(await del(new Response('', { status: 429 }))).toEqual({ ok: false, error: 'too_many', retryAfter: 60 });
    expect(await del(new Response('', { status: 500 }))).toEqual({ ok: false, error: 'failed' });
    expect(await del(new Response('', { status: 200 }))).toEqual({ ok: false, error: 'failed' });
    expect(await del(new Error('offline'))).toEqual({ ok: false, error: 'failed' });
  });
});

describe('fileName', () => {
  it('takes a plain name from the header and falls back otherwise', () => {
    expect(fileName('attachment; filename="ops-2026-10-14-0937.md"', 'ops')).toBe('ops-2026-10-14-0937.md');
    expect(fileName(null, 'ops')).toBe('ops-notes.md');
    expect(fileName('attachment; filename="../../etc/passwd"', 'ops')).toBe('ops-notes.md');
  });
});

describe('notesUntil', () => {
  it('reads a future RFC 3339 time and nothing else', () => {
    const now = Date.parse('2026-10-14T10:00:00Z');
    expect(notesUntil('2026-10-15T10:02:00+03:00', now)?.toISOString()).toBe('2026-10-15T07:02:00.000Z');
    expect(notesUntil('2026-10-14T09:00:00Z', now)).toBeNull();
    expect(notesUntil(undefined, now)).toBeNull();
    expect(notesUntil('soon', now)).toBeNull();
  });
});

describe('notesErrorText', () => {
  it('maps each failure to its message', () => {
    const tr = (key: string, vars?: Record<string, string | number>) => key + (vars ? JSON.stringify(vars) : '');
    expect(notesErrorText({ error: 'bad_pin' }, tr)).toBe('notes.badPin');
    expect(notesErrorText({ error: 'gone' }, tr)).toBe('notes.gone');
    expect(notesErrorText({ error: 'too_many', retryAfter: 42 }, tr)).toBe('pin.tooMany{"seconds":42}');
    expect(notesErrorText({ error: 'recording' }, tr)).toBe('notes.deleteRecording');
    expect(notesErrorText({ error: 'failed' }, tr)).toBe('notes.failed');
    expect(notesErrorText({ error: 'failed' }, tr, 'notes.deleteFailed')).toBe('notes.deleteFailed');
  });
});
