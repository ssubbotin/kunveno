// Installation details from GET /api/info, set per installation in config.yaml: the title and
// the reactions participants can send. Reactive, so the lobby and the room follow them.

export const DEFAULT_TITLE = 'kunveno';
// Must match config.DefaultReactions on the server.
export const DEFAULT_REACTIONS = ['👍', '👎', '👏', '😂', '❤️', '🎉', '😮', '🤔', '☕', '👋'];

export type Info = { title: string; reactions: string[] };

export function infoFrom(info: unknown): Info {
  const v = (info ?? {}) as { title?: unknown; reactions?: unknown };
  const title = typeof v.title === 'string' && v.title.trim() !== '' ? v.title.trim() : DEFAULT_TITLE;
  const list = Array.isArray(v.reactions)
    ? v.reactions.filter((e): e is string => typeof e === 'string' && e !== '' && new TextEncoder().encode(e).length <= 16)
    : [];
  return { title, reactions: list.length > 0 ? list : DEFAULT_REACTIONS };
}

class Site {
  title = $state(DEFAULT_TITLE);
  reactions = $state<string[]>(DEFAULT_REACTIONS);
}

export const site = new Site();

// loadInfo fetches the details and names the browser tab after the title; on failure the
// defaults stay and the lobby still works.
export async function loadInfo(): Promise<void> {
  try {
    const info = infoFrom(await (await fetch('/api/info')).json());
    site.title = info.title;
    site.reactions = info.reactions;
  } catch {
    // keep the defaults
  }
  document.title = site.title;
}
