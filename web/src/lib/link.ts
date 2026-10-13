// Links to a room: https://<host>/join/<slug>#pin=<PIN>. The room sits in the path, under /join/
// so that a slug such as "ws" or "api" never meets the paths nginx proxies, and the PIN in the
// fragment, which the browser keeps to itself: it reaches neither the server's logs nor a Referer.

const SLUG = /^[a-z0-9-]{1,32}$/;

export type JoinLink = { room: string; pin: string };

export function roomLink(origin: string, room: string, pin: string): string {
  return `${origin}/join/${room}#pin=${encodeURIComponent(pin)}`;
}

// Reads a join link from a location's pathname and hash; null for any other page.
export function parseJoinLink(pathname: string, hash: string): JoinLink | null {
  const m = /^\/join\/([^/]+)\/?$/.exec(pathname);
  if (!m || !SLUG.test(m[1])) return null;
  const pin = new URLSearchParams(hash.replace(/^#/, '')).get('pin') ?? '';
  return { room: m[1], pin };
}
