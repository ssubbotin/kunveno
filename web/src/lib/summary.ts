// The summary of a room's last meeting, written by the notes add-on from the meeting's notes and
// kept with them: its state in the room list, and downloading it (POST /api/rooms/<slug>/summary
// with the room PIN and the interface language, which the file's fixed labels follow). It is never
// deleted on its own: deleting the notes deletes it.
import { downloadFailure, fileName, type NotesResult } from './notes';

export type SummaryState = 'pending' | 'ready' | 'failed';
type Fetch = (url: string, init: RequestInit) => Promise<Response>;

// summaryState reads summary of a room list item; null when absent or unknown.
export function summaryState(v: unknown): SummaryState | null {
  return v === 'pending' || v === 'ready' || v === 'failed' ? v : null;
}

export async function fetchSummary(fetchFn: Fetch, slug: string, pin: string, lang: string): Promise<NotesResult> {
  let res: Response;
  try {
    res = await fetchFn(`/api/rooms/${encodeURIComponent(slug)}/summary`, {
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
