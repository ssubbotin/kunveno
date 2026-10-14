import { audioConstraints } from './micLevel';

// MicInput is the microphone as sent: the device's own track, or its sound through the noise
// filter. stop releases everything behind it.
export type MicInput = { track: MediaStreamTrack; deviceId: string; filtered: boolean; stop(): void };

// capture opens the microphone chosen in the lobby, or the default one if it is gone.
async function capture(deviceId: string, filter: boolean): Promise<MediaStreamTrack> {
  try {
    return (await navigator.mediaDevices.getUserMedia({ audio: audioConstraints(deviceId, filter) })).getAudioTracks()[0];
  } catch (e) {
    if (!deviceId || (e as Error).name !== 'OverconstrainedError') throw e;
    return (await navigator.mediaDevices.getUserMedia({ audio: audioConstraints('', filter) })).getAudioTracks()[0];
  }
}

// openMic opens the microphone, through the noise filter when asked. When the filter cannot start
// (an old browser, its files did not load) the microphone opens as usual with the browser's own
// noise suppression, and filtered says so.
export async function openMic(deviceId: string, filter: boolean): Promise<MicInput> {
  const raw = await capture(deviceId, filter);
  const id = raw.getSettings().deviceId ?? deviceId;
  if (filter) {
    try {
      const { filterTrack } = await import('./noiseFilter');
      const f = await filterTrack(raw);
      return { track: f.track, deviceId: id, filtered: true, stop: () => { f.stop(); raw.stop(); } };
    } catch (e) {
      console.warn('noise filter', e);
      raw.stop();
      const plain = await capture(deviceId, false);
      return { track: plain, deviceId: plain.getSettings().deviceId ?? id, filtered: false, stop: () => plain.stop() };
    }
  }
  return { track: raw, deviceId: id, filtered: false, stop: () => raw.stop() };
}
