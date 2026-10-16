// Guest invitations of a room (sign-in on): create a link, list the usable ones, revoke one. Every
// request carries the room's PIN; a refusal becomes an InviteFailure that names its text.
import type { Key } from './i18n/messages';

// Lifetimes in hours the server accepts (1 h, 24 h, 7 days); 24 is preselected. The server may
// allow less (auth.invite_max) and refuses a longer one with 400.
export const INVITE_HOURS = [1, 24, 168] as const;
export const DEFAULT_HOURS = 24;

export type Invite = { id: string; createdBy: string; expires: Date };
export type Created = { id: string; link: string; expires: Date };

export class InviteFailure extends Error {
  constructor(readonly key: Key, readonly vars: Record<string, number> = {}) {
    super(key);
  }
}

// failureFor names the text for a refused request.
export function failureFor(status: number, retryAfter: string | null): InviteFailure {
  switch (status) {
    case 401:
      return new InviteFailure('auth.expired');
    case 403:
      return new InviteFailure('error.bad_pin');
    case 400:
      return new InviteFailure('invite.tooLong');
    case 429: {
      const s = Number(retryAfter);
      return new InviteFailure('pin.tooMany', { seconds: Number.isFinite(s) && s > 0 ? Math.ceil(s) : 60 });
    }
    default:
      return new InviteFailure('invite.failed');
  }
}

function date(v: unknown): Date | null {
  if (typeof v !== 'string') return null;
  const d = new Date(v);
  return Number.isNaN(d.getTime()) ? null : d;
}

// parseInvites keeps the well-formed entries of the list.
export function parseInvites(v: unknown): Invite[] {
  if (!Array.isArray(v)) return [];
  const out: Invite[] = [];
  for (const e of v as { id?: unknown; created_by?: unknown; expires?: unknown }[]) {
    const expires = date(e?.expires);
    if (typeof e?.id === 'string' && typeof e.created_by === 'string' && expires) out.push({ id: e.id, createdBy: e.created_by, expires });
  }
  return out;
}

async function call(method: string, path: string, body: object): Promise<Response> {
  let res: Response;
  try {
    res = await fetch(path, { method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
  } catch {
    throw new InviteFailure('invite.failed');
  }
  if (!res.ok) throw failureFor(res.status, res.headers.get('Retry-After'));
  return res;
}

async function json(res: Response): Promise<unknown> {
  try {
    return await res.json();
  } catch {
    throw new InviteFailure('invite.failed');
  }
}

const base = (slug: string) => `/api/rooms/${encodeURIComponent(slug)}/invites`;

export async function createInvite(slug: string, pin: string, hours: number): Promise<Created> {
  const v = (await json(await call('POST', base(slug), { pin, hours }))) as { id?: unknown; link?: unknown; expires?: unknown };
  const expires = date(v?.expires);
  if (typeof v?.id !== 'string' || typeof v.link !== 'string' || !expires) throw new InviteFailure('invite.failed');
  return { id: v.id, link: v.link, expires };
}

export async function listInvites(slug: string, pin: string): Promise<Invite[]> {
  return parseInvites(await json(await call('POST', `${base(slug)}/list`, { pin })));
}

export async function revokeInvite(slug: string, pin: string, id: string): Promise<void> {
  await call('DELETE', `${base(slug)}/${encodeURIComponent(id)}`, { pin });
}
