// Downloading the notes of a room's last meeting: POST /api/rooms/<slug>/notes with the room PIN and
// the interface language (the file's fixed labels follow it), saved through a temporary link.
import type { Key } from './i18n/messages';

export type NotesError = 'bad_pin' | 'gone' | 'too_many' | 'failed';
export type NotesResult = { ok: true; blob: Blob; filename: string } | { ok: false; error: NotesError; retryAfter?: number };
type Fetch = (url: string, init: RequestInit) => Promise<Response>;
type Translate = (key: Key, vars?: Record<string, string | number>) => string;

export async function fetchNotes(fetchFn: Fetch, slug: string, pin: string, lang: string): Promise<NotesResult> {
  let res: Response;
  try {
    res = await fetchFn(`/api/rooms/${encodeURIComponent(slug)}/notes`, {
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
  if (res.status === 403) return { ok: false, error: 'bad_pin' };
  if (res.status === 404) return { ok: false, error: 'gone' };
  if (res.status === 429) return { ok: false, error: 'too_many', retryAfter: Number(res.headers.get('Retry-After')) || 60 };
  return { ok: false, error: 'failed' };
}

// fileName takes the server's file name when it is a plain one.
export function fileName(disposition: string | null, slug: string): string {
  const m = /filename="([^"]+)"/.exec(disposition ?? '');
  return m && /^[\w.-]+$/.test(m[1]) && !m[1].startsWith('.') ? m[1] : `${slug}-notes.md`;
}

// notesUntil reads notes_until of a room list item: the time, or null when absent, malformed or past.
export function notesUntil(v: unknown, now = Date.now()): Date | null {
  if (typeof v !== 'string') return null;
  const d = new Date(v);
  return Number.isNaN(d.getTime()) || d.getTime() <= now ? null : d;
}

export function notesErrorText(r: { error: NotesError; retryAfter?: number }, tr: Translate): string {
  switch (r.error) {
    case 'bad_pin':
      return tr('notes.badPin');
    case 'gone':
      return tr('notes.gone');
    case 'too_many':
      return tr('pin.tooMany', { seconds: r.retryAfter ?? 60 });
    default:
      return tr('notes.failed');
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
