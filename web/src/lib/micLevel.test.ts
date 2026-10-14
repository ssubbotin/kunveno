import { describe, expect, it } from 'vitest';
import { audioConstraints, dbfs, meterPercent, noiseFilterOn } from './micLevel';

describe('mic level', () => {
  it('converts RMS to dBFS', () => {
    expect(dbfs(1)).toBeCloseTo(0);
    expect(dbfs(0.1)).toBeCloseTo(-20);
    expect(dbfs(0)).toBe(-Infinity);
  });

  it('maps -60..0 dBFS onto the meter, clamped', () => {
    expect(meterPercent(-60)).toBe(0);
    expect(meterPercent(-30)).toBe(50);
    expect(meterPercent(0)).toBe(100);
    expect(meterPercent(-Infinity)).toBe(0);
    expect(meterPercent(6)).toBe(100);
  });
});

describe('audioConstraints', () => {
  it('keeps the voice processing and pins the chosen device', () => {
    expect(audioConstraints('abc')).toEqual({ echoCancellation: true, noiseSuppression: true, autoGainControl: true, deviceId: { exact: 'abc' } });
  });
  it('lets the browser choose when nothing was picked', () => {
    expect(audioConstraints('')).toEqual({ echoCancellation: true, noiseSuppression: true, autoGainControl: true });
  });
});

describe('noise filter', () => {
  it('lets the participant choice win over the site default', () => {
    expect(noiseFilterOn(undefined, false)).toBe(false);
    expect(noiseFilterOn(undefined, true)).toBe(true);
    expect(noiseFilterOn(true, false)).toBe(true);
    expect(noiseFilterOn(false, true)).toBe(false);
  });
  it('drops the browser noise suppression when the filter does the job', () => {
    expect(audioConstraints('', true)).toEqual({ echoCancellation: true, noiseSuppression: false, autoGainControl: true });
    expect(audioConstraints('mic1')).toEqual({ echoCancellation: true, noiseSuppression: true, autoGainControl: true, deviceId: { exact: 'mic1' } });
  });
});
