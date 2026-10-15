// What is left of a room's last meeting: its video (recording) and its notes, as the lobby shows
// them under one "Last meeting" button and one dialog.
import type { Key } from './i18n/messages';
import { notesUntil } from './notes';
import { recordingUntil } from './recording';

export type LastKind = 'video' | 'notes';
export type LastItem = { kind: LastKind; until: Date };

// lastItems lists what the room keeps, video first: an item counts when its feature is on for the
// site and its expiry is a valid future time.
export function lastItems(
  room: { notes_until?: unknown; recording_until?: unknown } | undefined,
  flags: { notesEnabled: boolean; recordingEnabled: boolean },
  now = Date.now(),
): LastItem[] {
  if (!room) return [];
  const items: LastItem[] = [];
  const video = flags.recordingEnabled ? recordingUntil(room.recording_until, now) : null;
  if (video) items.push({ kind: 'video', until: video });
  const notes = flags.notesEnabled ? notesUntil(room.notes_until, now) : null;
  if (notes) items.push({ kind: 'notes', until: notes });
  return items;
}

// lastCaption says what is there and the nearest expiry; null when there is nothing.
export function lastCaption(items: LastItem[]): { key: Key; until: Date } | null {
  if (items.length === 0) return null;
  const until = new Date(Math.min(...items.map((i) => i.until.getTime())));
  const kinds = new Set(items.map((i) => i.kind));
  const key: Key = kinds.has('video') && kinds.has('notes') ? 'last.both' : kinds.has('video') ? 'last.video' : 'last.notes';
  return { key, until };
}

// formatUntil writes an expiry as day, month and time, with the year only when it is not the
// current one: "16.10, 14:43", "16.10.2027, 14:43" in Russian.
export function formatUntil(d: Date, locale: string, now = new Date()): string {
  const opts: Intl.DateTimeFormatOptions = { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' };
  if (d.getFullYear() !== now.getFullYear()) opts.year = 'numeric';
  return new Intl.DateTimeFormat(locale, opts).format(d);
}
