// The camera as sent: the device's own track, or its picture through the background effect.
import { cameraConstraints } from './media';
import type { BackgroundChoice } from './background/choice';
import { BackgroundUnavailable, startEffect, type Effect } from './background/effect';
import type { EffectMode } from './background/protocol';
import { presetUrl } from './background/presets';
import { loadOwn } from './background/ownImage';

export { BackgroundUnavailable };

export type CameraInput = { track: MediaStreamTrack; deviceId: string; effect: Effect | null; stop(): void };

async function capture(deviceId: string): Promise<MediaStreamTrack> {
  try {
    return (await navigator.mediaDevices.getUserMedia({ video: cameraConstraints(deviceId) })).getVideoTracks()[0];
  } catch (e) {
    if (!deviceId || (e as Error).name !== 'OverconstrainedError') throw e;
    return (await navigator.mediaDevices.getUserMedia({ video: cameraConstraints('') })).getVideoTracks()[0];
  }
}

// modeFor turns a choice into what the worker needs: blur, glass blocks, or an image as a bitmap.
export async function modeFor(choice: BackgroundChoice): Promise<EffectMode | null> {
  if (choice.kind === 'none') return null;
  if (choice.kind === 'blur') return { kind: 'blur' };
  if (choice.kind === 'glass') return { kind: 'glass' };
  try {
    const blob = choice.source === 'own' ? await loadOwn() : await (await fetch(presetUrl(choice.name))).blob();
    if (!blob) throw new Error('no image');
    return { kind: 'image', image: await createImageBitmap(blob) };
  } catch (e) {
    throw new BackgroundUnavailable(String(e));
  }
}

// openCamera opens the camera with the chosen background. If the background cannot start, the
// camera is closed again and BackgroundUnavailable is thrown: the room is never shown by accident.
export async function openCamera(deviceId: string, choice: BackgroundChoice, onStats?: (fps: number) => void): Promise<CameraInput> {
  const mode = await modeFor(choice);
  const raw = await capture(deviceId);
  const id = raw.getSettings().deviceId ?? deviceId;
  if (!mode) return { track: raw, deviceId: id, effect: null, stop: () => raw.stop() };
  try {
    const effect = await startEffect(raw, mode, onStats);
    return { track: effect.track, deviceId: id, effect, stop: () => { effect.stop(); raw.stop(); } };
  } catch (e) {
    raw.stop();
    if (mode.kind === 'image') mode.image.close();
    throw e instanceof BackgroundUnavailable ? e : new BackgroundUnavailable(String(e));
  }
}
