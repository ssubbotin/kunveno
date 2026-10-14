// Background presets from the server (GET /api/backgrounds).
export type Preset = { name: string; width: number; height: number };

export function presetsFrom(v: unknown): Preset[] {
  if (!Array.isArray(v)) return [];
  return v.filter((p): p is Preset =>
    !!p && typeof p.name === 'string' && p.name !== '' && Number.isInteger(p.width) && Number.isInteger(p.height));
}

// fetchPresets returns the server's presets, or null when the list could not be fetched: a failed
// request says nothing about which presets exist.
export async function fetchPresets(): Promise<Preset[] | null> {
  try {
    const res = await fetch('/api/backgrounds');
    if (!res.ok) return null;
    return presetsFrom(await res.json());
  } catch {
    return null;
  }
}

export const presetUrl = (name: string) => `/api/backgrounds/${encodeURIComponent(name)}`;
export const previewUrl = (name: string) => `${presetUrl(name)}/preview`;

// presetLabel names a preset after its file: no extension, dashes and underscores as spaces.
export function presetLabel(name: string): string {
  return name.replace(/\.[^.]+$/, '').replace(/[-_]+/g, ' ').trim();
}
