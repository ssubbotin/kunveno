// Camera and pre-join preferences.

type Store = Pick<Storage, 'getItem' | 'setItem'>;

// splitStream reads the stream id the server gives forwarded tracks: the participant id, with a
// "/camera" suffix for a camera (audio and the shared screen use the bare id).
export function splitStream(id: string): { participant: string; camera: boolean } {
  return id.endsWith('/camera') ? { participant: id.slice(0, -'/camera'.length), camera: true } : { participant: id, camera: false };
}

// cameraConstraints keeps the picture modest (640x360 at 15 fps): every camera goes to every
// other participant, so a full room multiplies its cost.
export function cameraConstraints(deviceId: string): MediaTrackConstraints {
  const c: MediaTrackConstraints = { width: { ideal: 640 }, height: { ideal: 360 }, frameRate: { ideal: 15, max: 15 } };
  if (deviceId) c.deviceId = { exact: deviceId };
  return c;
}

export const CAMERA_MAX_KBPS = 350;

export type Prefs = { micOn: boolean; cameraOn: boolean; receiveVideo: boolean; cameraId: string };

const KEY = 'kunveno.prefs';

// loadPrefs returns what the person chose before entering: microphone on, camera off and
// other cameras received unless they said otherwise.
export function loadPrefs(store: Store): Prefs {
  const d: Prefs = { micOn: true, cameraOn: false, receiveVideo: true, cameraId: '' };
  try {
    const v = JSON.parse(store.getItem(KEY) ?? '{}');
    return {
      micOn: typeof v.micOn === 'boolean' ? v.micOn : d.micOn,
      cameraOn: typeof v.cameraOn === 'boolean' ? v.cameraOn : d.cameraOn,
      receiveVideo: typeof v.receiveVideo === 'boolean' ? v.receiveVideo : d.receiveVideo,
      cameraId: typeof v.cameraId === 'string' ? v.cameraId : d.cameraId,
    };
  } catch {
    return d;
  }
}

export function savePrefs(store: Store, p: Prefs): void {
  store.setItem(KEY, JSON.stringify(p));
}
