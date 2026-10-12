// Microphone level for the lobby meter.

// dbfs converts an RMS amplitude (0..1) to decibels relative to full scale.
export function dbfs(rms: number): number {
  return rms > 0 ? 20 * Math.log10(rms) : -Infinity;
}

// meterPercent places a dBFS value on a -60..0 dB scale: speech at a normal distance sits
// around -30..-15 dB, room silence below -50 dB.
export function meterPercent(db: number): number {
  if (!Number.isFinite(db)) return 0;
  return Math.round(Math.min(100, Math.max(0, ((db + 60) / 60) * 100)));
}

// rmsOf measures one block of time-domain samples.
export function rmsOf(buf: Float32Array): number {
  let sum = 0;
  for (const v of buf) sum += v * v;
  return Math.sqrt(sum / buf.length);
}

export const MIC_KEY = 'kunveno.micId';

// audioConstraints asks for the chosen input, or the browser default when none was picked,
// with the browser's voice processing on.
export function audioConstraints(deviceId: string): MediaTrackConstraints {
  const c: MediaTrackConstraints = { echoCancellation: true, noiseSuppression: true, autoGainControl: true };
  if (deviceId) c.deviceId = { exact: deviceId };
  return c;
}
