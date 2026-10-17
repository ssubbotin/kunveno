// The meetings a room keeps (GET /api/rooms/<slug>/meetings): each with its notes, summary and
// recording parts, newest first. No names; the items are downloaded and deleted with the room PIN
// through the functions of notes.ts, summary.ts and recording.ts given the meeting.
import { summaryState, type SummaryState } from './summary';

type Fetch = (url: string, init?: RequestInit) => Promise<Response>;

export type MeetingPart = { start: Date; until: Date; size: number };
export type MeetingRow = {
  start: Date;
  end: Date | null;
  people: number;
  notes: Date | null; // until when the notes are kept; null when there are none
  summary: { state: SummaryState; until: Date } | null;
  parts: MeetingPart[];
};

const date = (v: unknown): Date | null => {
  if (typeof v !== 'string') return null;
  const d = new Date(v);
  return Number.isNaN(d.getTime()) ? null : d;
};

// parseMeetings reads the list, dropping rows without a valid start and fields that do not parse.
export function parseMeetings(v: unknown): MeetingRow[] {
  if (!Array.isArray(v)) return [];
  const out: MeetingRow[] = [];
  for (const raw of v) {
    if (typeof raw !== 'object' || raw === null) continue;
    const r = raw as Record<string, unknown>;
    const start = date(r.start);
    if (!start) continue;
    const state = summaryState(r.summary);
    const summaryUntil = date(r.summary_until);
    const parts: MeetingPart[] = [];
    for (const p of Array.isArray(r.parts) ? r.parts : []) {
      const q = (typeof p === 'object' && p !== null ? p : {}) as Record<string, unknown>;
      const ps = date(q.start);
      const pu = date(q.until);
      if (ps && pu) parts.push({ start: ps, until: pu, size: typeof q.size === 'number' ? q.size : 0 });
    }
    parts.sort((a, b) => a.start.getTime() - b.start.getTime());
    out.push({
      start,
      end: date(r.end),
      people: typeof r.people === 'number' && Number.isInteger(r.people) && r.people > 0 ? r.people : 0,
      notes: date(r.notes_until),
      summary: state && summaryUntil ? { state, until: summaryUntil } : null,
      parts,
    });
  }
  return out;
}

// fetchMeetings loads the room's meetings; null when they cannot be had.
export async function fetchMeetings(fetchFn: Fetch, slug: string): Promise<MeetingRow[] | null> {
  try {
    const res = await fetchFn(`/api/rooms/${encodeURIComponent(slug)}/meetings`);
    if (!res.ok) return null;
    return parseMeetings(await res.json());
  } catch {
    return null;
  }
}
