// The summary of a room's last meeting, written by the notes add-on from the meeting's notes and
// kept with them: its state in the room list, downloading it (POST /api/rooms/<slug>/summary with
// the room PIN and the interface language, which the file's fixed labels follow) and deleting it
// alone (DELETE on the same path); deleting the notes deletes it too.
import { meetingPath } from './meetingPath';
import { downloadFailure, fileName, type DeleteResult, type NotesResult } from './notes';

export type SummaryState = 'pending' | 'ready' | 'failed';
type Fetch = (url: string, init: RequestInit) => Promise<Response>;

// summaryState reads summary of a room list item; null when absent or unknown.
export function summaryState(v: unknown): SummaryState | null {
  return v === 'pending' || v === 'ready' || v === 'failed' ? v : null;
}

// The summary of one meeting when meeting is given; the room's latest otherwise.
const summaryPath = (slug: string, meeting?: number) => (meeting !== undefined ? `${meetingPath(slug, meeting)}/summary` : `/api/rooms/${encodeURIComponent(slug)}/summary`);

export async function fetchSummary(fetchFn: Fetch, slug: string, pin: string, lang: string, meeting?: number): Promise<NotesResult> {
  let res: Response;
  try {
    res = await fetchFn(summaryPath(slug, meeting), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ pin, lang }),
    });
  } catch {
    return { ok: false, error: 'failed' };
  }
  if (!res.ok) return downloadFailure(res);
  try {
    return { ok: true, blob: await res.blob(), filename: fileName(res.headers.get('Content-Disposition'), slug, 'summary') };
  } catch {
    return { ok: false, error: 'failed' };
  }
}

// deleteSummary deletes the summary and keeps the notes; it is not written again for them.
export async function deleteSummary(fetchFn: Fetch, slug: string, pin: string, meeting?: number): Promise<DeleteResult> {
  let res: Response;
  try {
    res = await fetchFn(summaryPath(slug, meeting), {
      method: 'DELETE',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ pin }),
    });
  } catch {
    return { ok: false, error: 'failed' };
  }
  if (res.status === 204) return { ok: true };
  return downloadFailure(res);
}
