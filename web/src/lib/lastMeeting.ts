// What is left of a room's last meeting: its video (recording) and its notes, as the lobby shows
// them under one "Last meeting" button and one dialog.
import type { Key } from './i18n/messages';
import { notesUntil } from './notes';
import { recordingUntil } from './recording';
import { summaryState, type SummaryState } from './summary';

type Translate = (key: Key, vars?: Record<string, string | number>) => string;

export type LastKind = 'video' | 'notes' | 'summary';
// state: the summary's. The summary lives apart from the notes, with an expiry of its own.
export type LastItem = { kind: LastKind; until: Date; state?: SummaryState };

// lastItems lists what the room keeps, video first: an item counts when its feature is on for the
// site and its expiry is a valid future time. The meeting's summary counts with notes on, whether
// or not the notes are still there.
export function lastItems(
  room: { notes_until?: unknown; recording_until?: unknown; summary?: unknown; summary_until?: unknown } | undefined,
  flags: { notesEnabled: boolean; recordingEnabled: boolean },
  now = Date.now(),
): LastItem[] {
  if (!room) return [];
  const items: LastItem[] = [];
  const video = flags.recordingEnabled ? recordingUntil(room.recording_until, now) : null;
  if (video) items.push({ kind: 'video', until: video });
  const notes = flags.notesEnabled ? notesUntil(room.notes_until, now) : null;
  if (notes) items.push({ kind: 'notes', until: notes });
  const state = flags.notesEnabled ? summaryState(room.summary) : null;
  const summaryUntil = state ? notesUntil(room.summary_until, now) : null;
  if (state && summaryUntil) items.push({ kind: 'summary', until: summaryUntil, state });
  return items;
}

// lastCaption says what is there and the nearest expiry; null when there is nothing.
export function lastCaption(items: LastItem[]): { key: Key; until: Date } | null {
  if (items.length === 0) return null;
  // The video and the notes name the caption; the summary does only when nothing else is left.
  const main = items.filter((i) => i.kind !== 'summary');
  const shown = main.length > 0 ? main : items;
  const until = new Date(Math.min(...shown.map((i) => i.until.getTime())));
  const kinds = new Set(shown.map((i) => i.kind));
  const key: Key = main.length === 0 ? 'last.summary'
    : kinds.has('video') && kinds.has('notes') ? 'last.both' : kinds.has('video') ? 'last.video' : 'last.notes';
  return { key, until };
}

// formatUntil writes an expiry as day, month and time, with the year only when it is not the
// current one: "16.10, 14:43", "16.10.2027, 14:43" in Russian, "10/16, 2:43 PM" in English. A
// 24-hour clock keeps two-digit hours ("09:05"); a 12-hour one drops the leading zero.
export function formatUntil(d: Date, locale: string, now = new Date()): string {
  const opts: Intl.DateTimeFormatOptions = { day: '2-digit', month: '2-digit', hour: 'numeric', minute: '2-digit' };
  if (!new Intl.DateTimeFormat(locale, opts).resolvedOptions().hour12) opts.hour = '2-digit';
  if (d.getFullYear() !== now.getFullYear()) opts.year = 'numeric';
  return new Intl.DateTimeFormat(locale, opts).format(d);
}

// A summary of the meeting whose video or notes are kept: no names, only times and a count.
export type MeetingSummary = { start: Date; end: Date; people: number };

// lastSummary reads a room's last_meeting; null when it is missing or malformed (a bad date, an end
// before the start, a count that is not a positive whole number).
export function lastSummary(v: unknown): MeetingSummary | null {
  if (typeof v !== 'object' || v === null) return null;
  const { start, end, people } = v as Record<string, unknown>;
  if (typeof start !== 'string' || typeof end !== 'string' || typeof people !== 'number') return null;
  const s = new Date(start);
  const e = new Date(end);
  if (Number.isNaN(s.getTime()) || Number.isNaN(e.getTime()) || e < s) return null;
  if (!Number.isInteger(people) || people < 1) return null;
  return { start: s, end: e, people };
}

// formatTime writes a time of day by the same rules as formatUntil.
export function formatTime(d: Date, locale: string): string {
  const opts: Intl.DateTimeFormatOptions = { hour: 'numeric', minute: '2-digit' };
  if (!new Intl.DateTimeFormat(locale, opts).resolvedOptions().hour12) opts.hour = '2-digit';
  return new Intl.DateTimeFormat(locale, opts).format(d);
}

const sameDay = (a: Date, b: Date) =>
  a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();

// formatDuration counts the minutes between the times as they read (11:00 to 11:27 is 27 minutes);
// a meeting shorter than a minute is "<1 min".
export function formatDuration(start: Date, end: Date, t: Translate): string {
  if (end.getTime() - start.getTime() < 60_000) return t('last.underMinute');
  const minutes = Math.floor(end.getTime() / 60_000) - Math.floor(start.getTime() / 60_000);
  const h = Math.floor(minutes / 60);
  const m = minutes % 60;
  if (h === 0) return t('last.minutes', { n: m });
  return m === 0 ? t('last.hours', { n: h }) : t('last.hoursMinutes', { h, m });
}

// formatSummary writes "15.10, 11:00–11:27 · 27 мин · 7 участников": the end carries its own date
// when the meeting crossed midnight.
export function formatSummary(m: MeetingSummary, locale: string, t: Translate, now = new Date()): string {
  const end = sameDay(m.start, m.end) ? formatTime(m.end, locale) : formatUntil(m.end, locale, now);
  return `${formatUntil(m.start, locale, now)}–${end} · ${formatDuration(m.start, m.end, t)} · ${t('last.people', { n: m.people })}`;
}
