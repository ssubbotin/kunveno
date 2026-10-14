// Whether a stored background choice is gone: the server image removed, or the own image deleted.
// Only a definite answer counts; when the list or this browser's storage cannot be read, the choice
// is kept, and opening the camera with it fails safely if the image is really gone.
import { resolveChoice, type BackgroundChoice } from './choice';
import { fetchPresets } from './presets';
import { hasOwn } from './ownImage';

export async function isLost(c: BackgroundChoice): Promise<boolean> {
  if (c.kind !== 'image') return false;
  if (c.source === 'own') {
    const has = await hasOwn();
    return has !== null && resolveChoice(c, [], has).lost;
  }
  const presets = await fetchPresets();
  return presets !== null && resolveChoice(c, presets.map((p) => p.name), false).lost;
}
