// The meetings a room keeps (GET /api/rooms/<slug>/meetings): each with its notes, summary,
// recording parts and documents, newest first. No names; the items are downloaded and deleted with
// the room PIN through the functions of notes.ts, summary.ts, recording.ts and documents.ts given
// the meeting.
import { summaryState, type SummaryState } from './summary';

type Fetch = (url: string, init?: RequestInit) => Promise<Response>;

// part and meeting are the exact starts in Unix ms that name them in the routes.
export type MeetingPart = { part: number; start: Date; until: Date; size: number };
// A meeting document: id names it in the routes; until is when it expires.
export type MeetingDocument = { id: string; title: string; rev: number; updated: Date; until: Date };
const DOCUMENT_ID = /^[a-z0-9-]{1,40}$/;
export type MeetingRow = {
  meeting: number;
  start: Date;
  end: Date | null;
  people: number;
  notes: Date | null; // until when the notes are kept; null when there are none
  summary: { state: SummaryState; until: Date } | null;
  parts: MeetingPart[];
  documents: MeetingDocument[]; // in the order first stored; empty while documents are off
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
    if (!start || !Number.isSafeInteger(r.meeting)) continue;
    const state = summaryState(r.summary);
    const summaryUntil = date(r.summary_until);
    const parts: MeetingPart[] = [];
    for (const p of Array.isArray(r.parts) ? r.parts : []) {
      const q = (typeof p === 'object' && p !== null ? p : {}) as Record<string, unknown>;
      const ps = date(q.start);
      const pu = date(q.until);
      if (ps && pu && Number.isSafeInteger(q.part)) parts.push({ part: q.part as number, start: ps, until: pu, size: typeof q.size === 'number' ? q.size : 0 });
    }
    parts.sort((a, b) => a.start.getTime() - b.start.getTime());
    const documents: MeetingDocument[] = [];
    for (const d of Array.isArray(r.documents) ? r.documents : []) {
      const q = (typeof d === 'object' && d !== null ? d : {}) as Record<string, unknown>;
      const updated = date(q.updated);
      const until = date(q.until);
      if (typeof q.id === 'string' && DOCUMENT_ID.test(q.id) && typeof q.title === 'string' && q.title.trim() !== '' &&
        Number.isInteger(q.rev) && (q.rev as number) > 0 && updated && until) {
        documents.push({ id: q.id, title: q.title, rev: q.rev as number, updated, until });
      }
    }
    out.push({
      meeting: r.meeting as number,
      start,
      end: date(r.end),
      people: typeof r.people === 'number' && Number.isInteger(r.people) && r.people > 0 ? r.people : 0,
      notes: date(r.notes_until),
      summary: state && summaryUntil ? { state, until: summaryUntil } : null,
      parts,
      documents,
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

// latestRecording is the newest meeting that has recording parts, with its parts; null when none.
export function latestRecording(rows: MeetingRow[]): { meeting: number; parts: MeetingPart[] } | null {
  const row = rows.find((r) => r.parts.length > 0);
  return row ? { meeting: row.meeting, parts: row.parts } : null;
}
