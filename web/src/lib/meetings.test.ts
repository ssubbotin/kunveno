import { describe, expect, it } from 'vitest';
import { fetchMeetings, latestRecording, parseMeetings } from './meetings';
import { deleteNotes, fetchNotes } from './notes';
import { deleteRecording, recordingLink } from './recording';
import { deleteSummary, fetchSummary } from './summary';

function fakeFetch(res: Response | Error) {
  const calls: { url: string; init?: RequestInit }[] = [];
  const fn = async (url: string, init?: RequestInit) => {
    calls.push({ url, init });
    if (res instanceof Error) throw res;
    return res.clone();
  };
  return { fn, calls };
}

const row = {
  meeting: 1792137300123, start: '2026-10-16T10:55:00+03:00', end: '2026-10-16T11:38:00+03:00', people: 8,
  notes_until: '2026-10-17T11:38:00+03:00', summary: 'ready', summary_until: '2026-10-17T11:38:00+03:00',
  parts: [
    { part: 1792138800456, start: '2026-10-16T11:20:00+03:00', until: '2026-10-17T11:40:00+03:00', size: 20 },
    { part: 1792137360789, start: '2026-10-16T10:56:00+03:00', until: '2026-10-17T11:20:00+03:00', size: 10 },
  ],
};

describe('parseMeetings', () => {
  it('reads rows, the parts by start', () => {
    const [m] = parseMeetings([row]);
    expect(m.start).toEqual(new Date(row.start));
    expect(m.end).toEqual(new Date(row.end));
    expect(m.people).toBe(8);
    expect(m.notes).toEqual(new Date(row.notes_until));
    expect(m.summary).toEqual({ state: 'ready', until: new Date(row.summary_until) });
    expect(m.parts.map((p) => p.size)).toEqual([10, 20]);
    expect(m.meeting).toBe(1792137300123);
    expect(m.parts.map((p) => p.part)).toEqual([1792137360789, 1792138800456]);
  });
  it('drops malformed rows and fields', () => {
    expect(parseMeetings('x')).toEqual([]);
    expect(parseMeetings([{ start: 'soon', parts: [] }])).toEqual([]);
    expect(parseMeetings([{ start: row.start, parts: [] }])).toEqual([]); // no meeting id
    const [m] = parseMeetings([{ meeting: 1, start: row.start, parts: [{ start: row.start, until: row.end }], summary: 'odd', notes_until: 'never' }]);
    expect(m.end).toBeNull();
    expect(m.people).toBe(0);
    expect(m.notes).toBeNull();
    expect(m.summary).toBeNull();
    expect(m.parts).toEqual([]);
  });
});

describe('fetchMeetings', () => {
  it('asks for the room and reads the rows', async () => {
    const f = fakeFetch(new Response(JSON.stringify([row]), { status: 200 }));
    const rows = await fetchMeetings(f.fn, 'ops');
    expect(f.calls[0].url).toBe('/api/rooms/ops/meetings');
    expect(rows?.length).toBe(1);
  });
  it('is null on a failure', async () => {
    expect(await fetchMeetings(fakeFetch(new Response('', { status: 404 })).fn, 'ops')).toBeNull();
    expect(await fetchMeetings(fakeFetch(new Error('offline')).fn, 'ops')).toBeNull();
  });
});

describe('item requests of one meeting', () => {
  const meeting = 1792137300123;
  const part = 1792138800456;
  const base = `/api/rooms/ops/meetings/${meeting}`;
  it('name the meeting, and keep the old paths without one', async () => {
    const ok = () => fakeFetch(new Response('x', { status: 200 }));
    const gone = () => fakeFetch(new Response(null, { status: 204 }));
    let f = ok();
    await fetchNotes(f.fn, 'ops', '1234', 'ru', meeting);
    expect(f.calls[0].url).toBe(`${base}/notes`);
    f = gone();
    await deleteNotes(f.fn, 'ops', '1234', meeting);
    expect(f.calls[0].url).toBe(`${base}/notes`);
    f = ok();
    await fetchSummary(f.fn, 'ops', '1234', 'ru', meeting);
    expect(f.calls[0].url).toBe(`${base}/summary`);
    f = gone();
    await deleteSummary(f.fn, 'ops', '1234', meeting);
    expect(f.calls[0].url).toBe(`${base}/summary`);
    f = gone();
    await deleteRecording(f.fn, 'ops', '1234', { meeting, part });
    expect(f.calls[0].url).toBe(`${base}/recording/${part}`);
    f = ok();
    await fetchNotes(f.fn, 'ops', '1234', 'ru');
    expect(f.calls[0].url).toBe('/api/rooms/ops/notes');
  });
  it('accept a part link only to this room', async () => {
    const f = fakeFetch(new Response(JSON.stringify({ url: '/api/rooms/ops/recording/abc' }), { status: 200 }));
    expect(await recordingLink(f.fn, 'ops', '1234', { meeting, part })).toEqual({ ok: true, url: '/api/rooms/ops/recording/abc' });
    expect(f.calls[0].url).toBe(`${base}/recording/${part}`);
  });
});

describe('latestRecording', () => {
  it('gives the newest meeting that has parts, with its parts', () => {
    const rows = parseMeetings([
      { meeting: 3, start: '2026-10-17T10:00:00Z', parts: [] },
      { meeting: 2, start: '2026-10-16T10:00:00Z', parts: [
        { part: 22, start: '2026-10-16T10:30:00Z', until: '2026-10-18T10:00:00Z', size: 1 },
        { part: 21, start: '2026-10-16T10:01:00Z', until: '2026-10-18T10:00:00Z', size: 1 },
      ] },
    ]);
    const r = latestRecording(rows);
    expect(r?.meeting).toBe(2);
    expect(r?.parts.map((p) => p.part)).toEqual([21, 22]);
    expect(latestRecording([])).toBeNull();
  });
});
