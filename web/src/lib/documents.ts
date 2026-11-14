// Meeting documents (documents.enabled): Markdown files the facilitator prepares during a meeting.
// The room hears their list; each is downloaded with the room PIN (POST /api/rooms/<slug>/documents/<id>
// for the room's latest, or under one meeting of the history) and deleted from the history (DELETE).
import type { Key } from './i18n/messages';
import { meetingPath } from './meetingPath';
import { downloadFailure, fileName, type DeleteResult, type NotesError, type NotesResult } from './notes';

type Fetch = (url: string, init: RequestInit) => Promise<Response>;
type Translate = (key: Key, vars?: Record<string, string | number>) => string;

const documentPath = (slug: string, id: string, meeting?: number) =>
  `${meeting !== undefined ? meetingPath(slug, meeting) : `/api/rooms/${encodeURIComponent(slug)}`}/documents/${encodeURIComponent(id)}`;

export async function fetchDocument(fetchFn: Fetch, slug: string, pin: string, id: string, meeting?: number): Promise<NotesResult> {
  let res: Response;
  try {
    res = await fetchFn(documentPath(slug, id, meeting), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ pin }),
    });
  } catch {
    return { ok: false, error: 'failed' };
  }
  if (!res.ok) return downloadFailure(res);
  try {
    return { ok: true, blob: await res.blob(), filename: documentFileName(res.headers.get('Content-Disposition'), slug, id) };
  } catch {
    return { ok: false, error: 'failed' };
  }
}

export async function deleteDocument(fetchFn: Fetch, slug: string, pin: string, id: string, meeting: number): Promise<DeleteResult> {
  let res: Response;
  try {
    res = await fetchFn(documentPath(slug, id, meeting), {
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

// documentFileName takes the server's UTF-8 name (filename*, the title may be in any script) when it
// is a plain file name; then the plain one; otherwise <slug>-<id>.md.
export function documentFileName(disposition: string | null, slug: string, id: string): string {
  const m = /filename\*=UTF-8''([^;\s]+)/i.exec(disposition ?? '');
  if (m) {
    let name = '';
    try {
      name = decodeURIComponent(m[1]);
    } catch {
      name = '';
    }
    if (/^[\p{L}\p{N}._-]{1,200}$/u.test(name) && !name.startsWith('.')) return name;
    return `${slug}-${id}.md`;
  }
  return fileName(disposition, slug, id);
}

// documentErrorText words a failure; failed is the text of a failure without a reason.
export function documentErrorText(r: { error: NotesError; retryAfter?: number }, tr: Translate, failed: Key = 'documents.failed'): string {
  switch (r.error) {
    case 'bad_pin':
      return tr('notes.badPin');
    case 'gone':
      return tr('documents.gone');
    case 'too_many':
      return tr('pin.tooMany', { seconds: r.retryAfter ?? 60 });
    default:
      return tr(failed);
  }
}
