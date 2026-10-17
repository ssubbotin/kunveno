// The notes of a room's last meeting: downloading them (POST /api/rooms/<slug>/notes with the room
// PIN and the interface language, which the file's fixed labels follow, saved through a temporary
// link) and deleting them (DELETE on the same path with the room PIN).
import type { Key } from './i18n/messages';
import { meetingPath } from './meetingPath';

export type NotesError = 'bad_pin' | 'gone' | 'recording' | 'too_many' | 'failed';
export type NotesResult = { ok: true; blob: Blob; filename: string } | { ok: false; error: NotesError; retryAfter?: number };
type Fetch = (url: string, init: RequestInit) => Promise<Response>;
type Translate = (key: Key, vars?: Record<string, string | number>) => string;

// The notes of one meeting when meeting is given; the room's latest otherwise.
const notesPath = (slug: string, meeting?: number) => (meeting !== undefined ? `${meetingPath(slug, meeting)}/notes` : `/api/rooms/${encodeURIComponent(slug)}/notes`);

export async function fetchNotes(fetchFn: Fetch, slug: string, pin: string, lang: string, meeting?: number): Promise<NotesResult> {
  let res: Response;
  try {
    res = await fetchFn(notesPath(slug, meeting), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ pin, lang }),
    });
  } catch {
    return { ok: false, error: 'failed' };
  }
  if (res.ok) {
    try {
      return { ok: true, blob: await res.blob(), filename: fileName(res.headers.get('Content-Disposition'), slug) };
    } catch {
      return { ok: false, error: 'failed' };
    }
  }
  return downloadFailure(res);
}

export type DeleteResult = { ok: true } | { ok: false; error: NotesError; retryAfter?: number };

// deleteNotes deletes the room's last meeting notes; 'recording' while they are still being taken.
export async function deleteNotes(fetchFn: Fetch, slug: string, pin: string, meeting?: number): Promise<DeleteResult> {
  let res: Response;
  try {
    res = await fetchFn(notesPath(slug, meeting), {
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

// downloadFailure names a failed answer of the notes and summary requests.
export function downloadFailure(res: Response): { ok: false; error: NotesError; retryAfter?: number } {
  if (res.status === 403) return { ok: false, error: 'bad_pin' };
  if (res.status === 404) return { ok: false, error: 'gone' };
  if (res.status === 409) return { ok: false, error: 'recording' };
  if (res.status === 429) return { ok: false, error: 'too_many', retryAfter: Number(res.headers.get('Retry-After')) || 60 };
  return { ok: false, error: 'failed' };
}

// fileName takes the server's file name when it is a plain one; otherwise <slug>-<what>.md.
export function fileName(disposition: string | null, slug: string, what = 'notes'): string {
  const m = /filename="([^"]+)"/.exec(disposition ?? '');
  return m && /^[\w.-]+$/.test(m[1]) && !m[1].startsWith('.') ? m[1] : `${slug}-${what}.md`;
}

// notesUntil reads notes_until of a room list item: the time, or null when absent, malformed or past.
export function notesUntil(v: unknown, now = Date.now()): Date | null {
  if (typeof v !== 'string') return null;
  const d = new Date(v);
  return Number.isNaN(d.getTime()) || d.getTime() <= now ? null : d;
}

// notesErrorText words a failure; failed is the text of a failure without a reason.
export function notesErrorText(r: { error: NotesError; retryAfter?: number }, tr: Translate, failed: Key = 'notes.failed'): string {
  switch (r.error) {
    case 'bad_pin':
      return tr('notes.badPin');
    case 'gone':
      return tr('notes.gone');
    case 'recording':
      return tr('notes.deleteRecording');
    case 'too_many':
      return tr('pin.tooMany', { seconds: r.retryAfter ?? 60 });
    default:
      return tr(failed);
  }
}

// saveFile hands a blob to the browser as a download.
export function saveFile(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
