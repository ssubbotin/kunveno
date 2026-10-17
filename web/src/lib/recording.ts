// The recording of a room's last meeting: a download link (POST /api/rooms/<slug>/recording with the
// room PIN; the file itself is a plain GET the browser downloads and can resume) and deletion
// (DELETE on the same path with the PIN).
import type { Key } from './i18n/messages';
import { meetingPath } from './meetingPath';

export type RecordingError = 'bad_pin' | 'gone' | 'recording' | 'too_many' | 'failed';
type Failure = { ok: false; error: RecordingError; retryAfter?: number };
type Fetch = (url: string, init: RequestInit) => Promise<Response>;
type Translate = (key: Key, vars?: Record<string, string | number>) => string;

const path = (slug: string) => `/api/rooms/${encodeURIComponent(slug)}/recording`;

// One part of one meeting's recording; without it, the room's latest part.
export type PartRef = { meeting: Date; part: Date };
const partPath = (slug: string, at?: PartRef) => (at ? `${meetingPath(slug, at.meeting)}/recording/${at.part.getTime()}` : path(slug));

export async function recordingLink(fetchFn: Fetch, slug: string, pin: string, at?: PartRef): Promise<{ ok: true; url: string } | Failure> {
  let res: Response;
  try {
    res = await fetchFn(partPath(slug, at), { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ pin }) });
  } catch {
    return { ok: false, error: 'failed' };
  }
  if (!res.ok) return failure(res);
  try {
    const v = (await res.json()) as { url?: unknown };
    // Only a link to this room's recording on this site.
    if (typeof v.url === 'string' && v.url.startsWith(`${path(slug)}/`)) return { ok: true, url: v.url };
  } catch {
    // fall through
  }
  return { ok: false, error: 'failed' };
}

export async function deleteRecording(fetchFn: Fetch, slug: string, pin: string, at?: PartRef): Promise<{ ok: true } | Failure> {
  let res: Response;
  try {
    res = await fetchFn(partPath(slug, at), { method: 'DELETE', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ pin }) });
  } catch {
    return { ok: false, error: 'failed' };
  }
  if (res.status === 204) return { ok: true };
  return failure(res);
}

function failure(res: Response): Failure {
  if (res.status === 403) return { ok: false, error: 'bad_pin' };
  if (res.status === 404) return { ok: false, error: 'gone' };
  if (res.status === 409) return { ok: false, error: 'recording' };
  if (res.status === 429) return { ok: false, error: 'too_many', retryAfter: Number(res.headers.get('Retry-After')) || 60 };
  return { ok: false, error: 'failed' };
}

// recordingUntil reads recording_until of a room list item: the time, or null when absent,
// malformed or past.
export function recordingUntil(v: unknown, now = Date.now()): Date | null {
  if (typeof v !== 'string') return null;
  const d = new Date(v);
  return Number.isNaN(d.getTime()) || d.getTime() <= now ? null : d;
}

export function recordingErrorText(r: { error: RecordingError; retryAfter?: number }, tr: Translate, failed: Key = 'recording.failed'): string {
  switch (r.error) {
    case 'bad_pin':
      return tr('recording.badPin');
    case 'gone':
      return tr('recording.gone');
    case 'recording':
      return tr('recording.deleteRecording');
    case 'too_many':
      return tr('pin.tooMany', { seconds: r.retryAfter ?? 60 });
    default:
      return tr(failed);
  }
}

// startDownload hands the link to the browser, which downloads the file itself.
export function startDownload(url: string): void {
  const a = document.createElement('a');
  a.href = url;
  a.download = '';
  document.body.append(a);
  a.click();
  a.remove();
}
