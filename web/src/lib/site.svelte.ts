// Installation details from GET /api/info, set per installation in config.yaml: the title, the
// reactions participants can send and the default of the noise filter. Reactive, so the lobby and
// the room follow them.

export const DEFAULT_TITLE = 'kunveno';
// Must match config.DefaultReactions on the server.
export const DEFAULT_REACTIONS = ['👍', '👎', '👏', '😂', '❤️', '🎉', '😮', '🤔', '☕', '👋'];

export type Info = { title: string; reactions: string[]; noiseSuppression: boolean; notesEnabled: boolean; notesLive: boolean; recordingEnabled: boolean };

export function infoFrom(info: unknown): Info {
  const v = (info ?? {}) as { title?: unknown; reactions?: unknown; noise_suppression?: unknown; notes?: unknown; notes_live?: unknown; recording?: unknown };
  const title = typeof v.title === 'string' && v.title.trim() !== '' ? v.title.trim() : DEFAULT_TITLE;
  const list = Array.isArray(v.reactions)
    ? v.reactions.filter((e): e is string => typeof e === 'string' && e !== '' && new TextEncoder().encode(e).length <= 16)
    : [];
  const notesEnabled = v.notes === true;
  return {
    title, reactions: list.length > 0 ? list : DEFAULT_REACTIONS, noiseSuppression: v.noise_suppression === true,
    notesEnabled, notesLive: notesEnabled && v.notes_live === true, recordingEnabled: v.recording === true,
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
}

export const site = new Site();

// loadInfo fetches the details and names the browser tab after the title; on failure the
// defaults stay and the lobby still works.
export async function loadInfo(): Promise<void> {
  try {
    const info = infoFrom(await (await fetch('/api/info')).json());
    site.title = info.title;
    site.reactions = info.reactions;
    site.noiseSuppression = info.noiseSuppression;
    site.notesEnabled = info.notesEnabled;
    site.notesLive = info.notesLive;
    site.recordingEnabled = info.recordingEnabled;
  } catch {
    // keep the defaults
  }
  document.title = site.title;
}
