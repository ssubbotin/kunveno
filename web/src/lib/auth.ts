// Sign-in (auth.enabled on the server): who is signed in, the way to the provider and back, and the
// guest's invitation. With sign-in off none of this is used.
import type { JoinLink } from './link';
import { STARTUP_TIMEOUT_MS, timeoutSignal } from './site.svelte';

export type Me = { name: string; login: string; provider: string };

// The fragment of the page that started a sign-in (a room link's PIN): the browser drops it on the
// way through the provider, so it waits in sessionStorage.
const RETURN_HASH = 'kunveno.return-hash';
// A guest's invitation token, for reconnects and a reload of this tab; never in localStorage and
// never logged.
export const INVITE_KEY = 'kunveno.invite';
const ROOM_KEY = 'kunveno.room';
const MAX_NAME = 32;

// fetchMe returns the signed-in person; null without a session (401), on a malformed answer or
// when the server cannot be reached or does not answer within STARTUP_TIMEOUT_MS.
export async function fetchMe(): Promise<Me | null> {
  try {
    const res = await fetch('/auth/me', { cache: 'no-store', signal: timeoutSignal(STARTUP_TIMEOUT_MS) });
    if (!res.ok) return null;
    const v = (await res.json()) as Partial<Record<keyof Me, unknown>> | null;
    if (typeof v?.name !== 'string' || typeof v.login !== 'string' || typeof v.provider !== 'string') return null;
    return { name: v.name, login: v.login, provider: v.provider };
  } catch {
    return null;
  }
}

// signInURL is where the "Sign in with" button goes; the server comes back to the same page. The
// fragment is kept in storage, since only the path and query travel through the provider.
export function signInURL(type: string, location: Pick<Location, 'pathname' | 'search' | 'hash'>, storage: Storage): string {
  if (location.hash && location.hash !== '#') storage.setItem(RETURN_HASH, location.hash);
  else storage.removeItem(RETURN_HASH);
  return `/auth/${encodeURIComponent(type)}/start?return=${encodeURIComponent(location.pathname + location.search)}`;
}

// restoreHash puts the fragment saved by signInURL back on the address once, then forgets it. A
// page that has a fragment of its own keeps it.
export function restoreHash(location: Pick<Location, 'pathname' | 'search' | 'hash'>, storage: Storage, replace: (url: string) => void): void {
  const saved = storage.getItem(RETURN_HASH);
  if (saved === null) return;
  storage.removeItem(RETURN_HASH);
  if (location.hash === '' || location.hash === '#') replace(location.pathname + location.search + saved);
}

// signOut ends the session on the server and starts the page afresh.
export async function signOut(): Promise<void> {
  try {
    await fetch('/auth/logout', { method: 'POST' });
  } finally {
    location.reload();
  }
}

// fixedName is the name an employee joins with: the account's, at most 32 characters, the login
// when the account has no name. The server takes it from the account anyway.
export function fixedName(me: Me): string {
  const name = me.name.trim() || me.login.trim();
  return [...name].slice(0, MAX_NAME).join('');
}

// guestFrom tells a guest's visit: a link with an invitation (kept for this tab), or the same
// room's link after the token left the address bar or the tab was reloaded. Null otherwise.
export function guestFrom(link: JoinLink | null, storage: Storage): { room: string; invite: string } | null {
  if (!link) return null;
  if (link.invite) {
    storage.setItem(INVITE_KEY, link.invite);
    storage.setItem(ROOM_KEY, link.room);
    return { room: link.room, invite: link.invite };
  }
  const kept = storage.getItem(INVITE_KEY);
  if (link.pin === '' && kept && storage.getItem(ROOM_KEY) === link.room) return { room: link.room, invite: kept };
  return null;
}
