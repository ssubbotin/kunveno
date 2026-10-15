import { describe, expect, it } from 'vitest';
import { formatUntil, lastCaption, lastItems } from './lastMeeting';

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
});
