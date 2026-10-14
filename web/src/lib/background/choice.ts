// The background a participant chose, as stored in their preferences.
export type BackgroundChoice =
  | { kind: 'none' }
  | { kind: 'blur' }
  | { kind: 'image'; source: 'server'; name: string }
  | { kind: 'image'; source: 'own' };

export const NONE: BackgroundChoice = { kind: 'none' };

// parseChoice accepts a stored value only when it is one of the four shapes; a server image name
// is a plain file name.
export function parseChoice(v: unknown): BackgroundChoice {
  const c = (v ?? {}) as { kind?: unknown; source?: unknown; name?: unknown };
  if (c.kind === 'blur') return { kind: 'blur' };
  if (c.kind === 'image' && c.source === 'own') return { kind: 'image', source: 'own' };
  if (c.kind === 'image' && c.source === 'server' && typeof c.name === 'string' && /^[^/\\]+$/.test(c.name) && !c.name.startsWith('.')) {
    return { kind: 'image', source: 'server', name: c.name };
  }
  return NONE;
}

// resolveChoice drops a choice whose image is gone (removed from the server, or the own image
// deleted); lost tells the picker to say so.
export function resolveChoice(c: BackgroundChoice, presets: string[], hasOwn: boolean): { choice: BackgroundChoice; lost: boolean } {
  if (c.kind !== 'image') return { choice: c, lost: false };
  const ok = c.source === 'own' ? hasOwn : presets.includes(c.name);
  return ok ? { choice: c, lost: false } : { choice: NONE, lost: true };
}

// sameEffect: both choices run through the effect, so switching is a message to the worker; a
// switch to or from none reopens the camera.
export function sameEffect(a: BackgroundChoice, b: BackgroundChoice): boolean {
  return a.kind !== 'none' && b.kind !== 'none';
}
