// Installation details from GET /api/info, set per installation in config.yaml: the title, the
// reactions participants can send and the default of the noise filter. Reactive, so the lobby and
// the room follow them.

export const DEFAULT_TITLE = 'kunveno';
// Must match config.DefaultReactions on the server.
export const DEFAULT_REACTIONS = ['👍', '👎', '👏', '😂', '❤️', '🎉', '😮', '🤔', '☕', '👋'];

// Sign-in (auth.enabled) and a button per provider; off unless the server says so.
export type Provider = { type: string; label: string };
export type Auth = { enabled: boolean; providers: Provider[] };
const MAX_PROVIDERS = 8;
const MAX_LABEL = 40;
// A provider type names a path segment of /auth/<type>/start.
const PROVIDER_TYPE = /^[a-z0-9-]{1,32}$/;

export type Info = { title: string; reactions: string[]; noiseSuppression: boolean; notesEnabled: boolean; notesLive: boolean; recordingEnabled: boolean; reconnectGrace: number; auth: Auth };

// authFrom reads the sign-in block defensively: anything malformed means sign-in off.
function authFrom(v: unknown): Auth {
  const a = v as { enabled?: unknown; providers?: unknown } | null;
  if (typeof a !== 'object' || a === null || a.enabled !== true) return { enabled: false, providers: [] };
  const list = Array.isArray(a.providers) ? (a.providers as { type?: unknown; label?: unknown }[]) : [];
  const providers = list
    .filter((p) => typeof p?.type === 'string' && PROVIDER_TYPE.test(p.type) && typeof p.label === 'string' && p.label !== '' && [...p.label].length <= MAX_LABEL)
    .slice(0, MAX_PROVIDERS)
    .map((p) => ({ type: p.type as string, label: p.label as string }));
  return { enabled: true, providers };
}

// Seconds a disconnected peer connection may stay so before the session restarts; must match
// config.DefaultReconnectGrace on the server (media.reconnect_grace, 3..120).
export const DEFAULT_RECONNECT_GRACE = 15;

export function infoFrom(info: unknown): Info {
  const v = (info ?? {}) as { title?: unknown; reactions?: unknown; noise_suppression?: unknown; notes?: unknown; notes_live?: unknown; recording?: unknown; reconnect_grace?: unknown; auth?: unknown };
  const title = typeof v.title === 'string' && v.title.trim() !== '' ? v.title.trim() : DEFAULT_TITLE;
  const list = Array.isArray(v.reactions)
    ? v.reactions.filter((e): e is string => typeof e === 'string' && e !== '' && new TextEncoder().encode(e).length <= 16)
    : [];
  const notesEnabled = v.notes === true;
  const g = v.reconnect_grace;
  const reconnectGrace = typeof g === 'number' && Number.isInteger(g) && g >= 3 && g <= 120 ? g : DEFAULT_RECONNECT_GRACE;
  return {
    reconnectGrace,
    title, reactions: list.length > 0 ? list : DEFAULT_REACTIONS, noiseSuppression: v.noise_suppression === true,
    notesEnabled, notesLive: notesEnabled && v.notes_live === true, recordingEnabled: v.recording === true,
    auth: authFrom(v.auth),
  };
}

class Site {
  title = $state(DEFAULT_TITLE);
  reactions = $state<string[]>(DEFAULT_REACTIONS);
  noiseSuppression = $state(false);
  // Meeting notes (notes.enabled; the kunveno-notes add-on): false hides every notes control.
  notesEnabled = $state(false);
  notesLive = $state(false);
  // Meeting recording (recording.enabled; the kunveno-recorder add-on)
  recordingEnabled = $state(false);
  // Seconds before a disconnected media connection restarts the session (media.reconnect_grace).
  reconnectGrace = $state(DEFAULT_RECONNECT_GRACE);
  // Sign-in (auth.enabled): off keeps the lobby open to everyone, as without it.
  auth = $state<Auth>({ enabled: false, providers: [] });
}

export const site = new Site();

// loadInfo fetches the details and names the browser tab after the title; on failure the
// defaults stay and the lobby still works. Calls made while one is under way share it.
let loading: Promise<void> | null = null;
export function loadInfo(): Promise<void> {
  loading ??= fetchInfo().finally(() => (loading = null));
  return loading;
}

async function fetchInfo(): Promise<void> {
  try {
    const info = infoFrom(await (await fetch('/api/info')).json());
    site.title = info.title;
    site.reactions = info.reactions;
    site.noiseSuppression = info.noiseSuppression;
    site.notesEnabled = info.notesEnabled;
    site.notesLive = info.notesLive;
    site.recordingEnabled = info.recordingEnabled;
    site.reconnectGrace = info.reconnectGrace;
    site.auth = info.auth;
  } catch {
    // keep the defaults
  }
  document.title = site.title;
}
