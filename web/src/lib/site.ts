// Installation details from GET /api/info; the title is set per installation in config.yaml.

export const DEFAULT_TITLE = 'kunveno';

export function titleFrom(info: unknown): string {
  const t = (info as { title?: unknown } | null)?.title;
  return typeof t === 'string' && t.trim() !== '' ? t.trim() : DEFAULT_TITLE;
}

// loadTitle fetches the title and names the browser tab after it.
export async function loadTitle(): Promise<string> {
  let title = DEFAULT_TITLE;
  try {
    title = titleFrom(await (await fetch('/api/info')).json());
  } catch {
    // keep the default: the lobby still works without it
  }
  document.title = title;
  return title;
}
