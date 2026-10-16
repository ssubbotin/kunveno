import { describe, expect, it } from 'vitest';
import { translate } from './i18n/i18n.svelte';
import type { Lang } from './i18n/messages';
import { formatDuration, formatSummary, formatUntil, lastCaption, lastItems, lastSummary } from './lastMeeting';

const now = Date.parse('2026-10-15T08:00:00Z');
const both = { notesEnabled: true, recordingEnabled: true };
const video = '2026-10-16T08:00:00Z';
const notes = '2026-10-16T09:00:00Z';

describe('lastItems', () => {
  it('lists video first, then notes', () => {
    expect(lastItems({ recording_until: video, notes_until: notes }, both, now).map((i) => i.kind)).toEqual(['video', 'notes']);
  });
  it('lists only video or only notes', () => {
    expect(lastItems({ recording_until: video }, both, now).map((i) => i.kind)).toEqual(['video']);
    expect(lastItems({ notes_until: notes }, both, now).map((i) => i.kind)).toEqual(['notes']);
  });
  it('is empty with nothing kept, past or malformed times, or no room', () => {
    expect(lastItems({}, both, now)).toEqual([]);
    expect(lastItems({ recording_until: '2026-10-14T08:00:00Z', notes_until: 'soon' }, both, now)).toEqual([]);
    expect(lastItems(undefined, both, now)).toEqual([]);
  });
  it('follows the site flags', () => {
    const room = { recording_until: video, notes_until: notes };
    expect(lastItems(room, { notesEnabled: false, recordingEnabled: true }, now).map((i) => i.kind)).toEqual(['video']);
    expect(lastItems(room, { notesEnabled: true, recordingEnabled: false }, now).map((i) => i.kind)).toEqual(['notes']);
    expect(lastItems(room, { notesEnabled: false, recordingEnabled: false }, now)).toEqual([]);
  });
});

describe('lastItems with a summary', () => {
  it('adds the summary after the notes, with its state and the notes expiry', () => {
    const items = lastItems({ notes_until: notes, summary: 'pending' }, both, now);
    expect(items).toEqual([
      { kind: 'notes', until: new Date(notes) },
      { kind: 'summary', until: new Date(notes), state: 'pending' },
    ]);
  });
  it('has no summary without notes or with an unknown state', () => {
    expect(lastItems({ summary: 'ready' }, both, now)).toEqual([]);
    expect(lastItems({ notes_until: notes, summary: 'done' }, both, now).map((i) => i.kind)).toEqual(['notes']);
    expect(lastItems({ notes_until: notes, summary: 'ready' }, { notesEnabled: false, recordingEnabled: true }, now)).toEqual([]);
  });
  it('leaves the caption to the video and the notes', () => {
    expect(lastCaption(lastItems({ notes_until: notes, summary: 'ready' }, both, now))).toEqual({ key: 'last.notes', until: new Date(notes) });
  });
});

describe('lastCaption', () => {
  const v = { kind: 'video' as const, until: new Date(video) };
  const n = { kind: 'notes' as const, until: new Date(notes) };
  it('names both and takes the nearest expiry', () => {
    expect(lastCaption([v, n])).toEqual({ key: 'last.both', until: new Date(video) });
    expect(lastCaption([{ ...v, until: new Date(notes) }, { ...n, until: new Date(video) }])).toEqual({ key: 'last.both', until: new Date(video) });
  });
  it('names a single item', () => {
    expect(lastCaption([v])).toEqual({ key: 'last.video', until: new Date(video) });
    expect(lastCaption([n])).toEqual({ key: 'last.notes', until: new Date(notes) });
  });
  it('is null for nothing', () => {
    expect(lastCaption([])).toBeNull();
  });
});

describe('formatUntil', () => {
  it('omits the current year', () => {
    expect(formatUntil(new Date(2026, 9, 16, 14, 43), 'ru-RU', new Date(2026, 9, 15, 10, 0))).toBe('16.10, 14:43');
  });
  it('shows another year', () => {
    expect(formatUntil(new Date(2027, 9, 16, 14, 43), 'ru-RU', new Date(2026, 9, 15, 10, 0))).toBe('16.10.2027, 14:43');
  });
  it('shows the year across the new year boundary', () => {
    expect(formatUntil(new Date(2027, 0, 1, 9, 5), 'ru-RU', new Date(2026, 11, 31, 23, 0))).toBe('01.01.2027, 09:05');
    expect(formatUntil(new Date(2027, 0, 1, 9, 5), 'ru-RU', new Date(2027, 0, 1, 0, 30))).toBe('01.01, 09:05');
  });
  it('follows the language: 12-hour English, 24-hour Spanish', () => {
    expect(formatUntil(new Date(2026, 9, 16, 14, 43), 'en-US', new Date(2026, 9, 15, 10, 0))).toBe('10/16, 2:43 PM');
    expect(formatUntil(new Date(2026, 9, 16, 9, 5), 'en-US', new Date(2026, 9, 15, 10, 0))).toBe('10/16, 9:05 AM');
    expect(formatUntil(new Date(2026, 9, 16, 14, 43), 'es-ES', new Date(2026, 9, 15, 10, 0))).toBe('16/10, 14:43');
  });
});

const local = (d: number, h: number, m: number, s = 0) => new Date(2026, 9, d, h, m, s);
const tr = (lang: Lang) => (key: Parameters<typeof translate>[1], vars?: Record<string, string | number>) => translate(lang, key, vars);

describe('lastSummary', () => {
  const start = local(15, 11, 0).toISOString();
  const end = local(15, 11, 27).toISOString();
  it('reads a summary', () => {
    expect(lastSummary({ start, end, people: 7 })).toEqual({ start: new Date(start), end: new Date(end), people: 7 });
  });
  it('ignores malformed summaries', () => {
    expect(lastSummary(undefined)).toBeNull();
    expect(lastSummary(null)).toBeNull();
    expect(lastSummary('x')).toBeNull();
    expect(lastSummary({ end, people: 7 })).toBeNull();
    expect(lastSummary({ start: 'soon', end, people: 7 })).toBeNull();
    expect(lastSummary({ start, end: 'later', people: 7 })).toBeNull();
    expect(lastSummary({ start: end, end: start, people: 7 })).toBeNull();
    expect(lastSummary({ start, end, people: -1 })).toBeNull();
    expect(lastSummary({ start, end, people: 0 })).toBeNull();
    expect(lastSummary({ start, end, people: 2.5 })).toBeNull();
    expect(lastSummary({ start, end, people: '7' })).toBeNull();
  });
});

describe('formatDuration', () => {
  it('writes minutes and hours', () => {
    const ru = tr('ru');
    expect(formatDuration(local(15, 11, 0), local(15, 11, 0, 40), ru)).toBe('<1 мин');
    expect(formatDuration(local(15, 11, 0), local(15, 11, 27), ru)).toBe('27 мин');
    expect(formatDuration(local(15, 11, 0, 50), local(15, 11, 27, 10), ru)).toBe('27 мин'); // as the times read
    expect(formatDuration(local(15, 11, 0), local(15, 12, 5), ru)).toBe('1 ч 5 мин');
    expect(formatDuration(local(15, 11, 0), local(15, 13, 0), ru)).toBe('2 ч');
    expect(formatDuration(local(15, 11, 0), local(15, 12, 5), tr('en'))).toBe('1 h 5 min');
    expect(formatDuration(local(15, 11, 0), local(15, 11, 0, 5), tr('en'))).toBe('<1 min');
    expect(formatDuration(local(15, 11, 0), local(15, 11, 27), tr('es'))).toBe('27 min');
    expect(formatDuration(local(15, 11, 0), local(15, 12, 5), tr('es'))).toBe('1 h 5 min');
  });
});

describe('formatSummary', () => {
  const now = local(15, 12, 0);
  const meeting = (people: number) => ({ start: local(15, 11, 0), end: local(15, 11, 27), people });
  it('writes the date, the times, the duration and the people', () => {
    expect(formatSummary(meeting(7), 'ru-RU', tr('ru'), now)).toBe('15.10, 11:00–11:27 · 27 мин · 7 участников');
    expect(formatSummary(meeting(7), 'en-US', tr('en'), now)).toBe('10/15, 11:00 AM–11:27 AM · 27 min · 7 participants');
    expect(formatSummary(meeting(7), 'es-ES', tr('es'), now)).toBe('15/10, 11:00–11:27 · 27 min · 7 participantes');
  });
  it('uses the plural forms of each language', () => {
    const ru = (n: number) => formatSummary(meeting(n), 'ru-RU', tr('ru'), now).split(' · ')[2];
    expect([1, 2, 5, 11, 21, 22].map(ru)).toEqual(['1 участник', '2 участника', '5 участников', '11 участников', '21 участник', '22 участника']);
    const en = (n: number) => formatSummary(meeting(n), 'en-US', tr('en'), now).split(' · ')[2];
    expect([1, 2, 21].map(en)).toEqual(['1 participant', '2 participants', '21 participants']);
    const es = (n: number) => formatSummary(meeting(n), 'es-ES', tr('es'), now).split(' · ')[2];
    expect([1, 2, 21].map(es)).toEqual(['1 participante', '2 participantes', '21 participantes']);
  });
  it('dates the end of a meeting that crosses midnight', () => {
    const m = { start: local(15, 23, 40), end: local(16, 0, 25), people: 3 };
    expect(formatSummary(m, 'ru-RU', tr('ru'), now)).toBe('15.10, 23:40–16.10, 00:25 · 45 мин · 3 участника');
  });
  it('shows the year of a meeting in another year', () => {
    const m = { start: new Date(2025, 11, 31, 10, 0), end: new Date(2025, 11, 31, 10, 30), people: 2 };
    expect(formatSummary(m, 'ru-RU', tr('ru'), now)).toBe('31.12.2025, 10:00–10:30 · 30 мин · 2 участника');
  });
});
