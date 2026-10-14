// Background presets from the server (GET /api/backgrounds).
export type Preset = { name: string; width: number; height: number };

export function presetsFrom(v: unknown): Preset[] {
  if (!Array.isArray(v)) return [];
  return v.filter((p): p is Preset =>
    !!p && typeof p.name === 'string' && p.name !== '' && Number.isInteger(p.width) && Number.isInteger(p.height));
}

export async function fetchPresets(): Promise<Preset[]> {
  try {
    return presetsFrom(await (await fetch('/api/backgrounds')).json());
  } catch {
    return [];
  }
}

export const presetUrl = (name: string) => `/api/backgrounds/${encodeURIComponent(name)}`;
export const previewUrl = (name: string) => `${presetUrl(name)}/preview`;

// presetLabel names a preset after its file: no extension, dashes and underscores as spaces.
export function presetLabel(name: string): string {
  return name.replace(/\.[^.]+$/, '').replace(/[-_]+/g, ' ').trim();
}
