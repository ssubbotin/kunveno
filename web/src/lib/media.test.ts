import { describe, expect, it } from 'vitest';
import { cameraConstraints, loadPrefs, savePrefs, splitStream } from './media';

class MemoryStorage {
  private m = new Map<string, string>();
  getItem(k: string) { return this.m.get(k) ?? null; }
  setItem(k: string, v: string) { this.m.set(k, v); }
}

describe('splitStream', () => {
  it('tells a camera from audio and screen by the stream id', () => {
    expect(splitStream('ab12/camera')).toEqual({ participant: 'ab12', camera: true });
    expect(splitStream('ab12')).toEqual({ participant: 'ab12', camera: false });
  });
});

describe('cameraConstraints', () => {
  it('asks for a modest picture and pins the chosen device', () => {
    expect(cameraConstraints('cam1')).toEqual({ width: { ideal: 640 }, height: { ideal: 360 }, frameRate: { ideal: 15, max: 15 }, deviceId: { exact: 'cam1' } });
    expect(cameraConstraints('')).toEqual({ width: { ideal: 640 }, height: { ideal: 360 }, frameRate: { ideal: 15, max: 15 } });
  });
});

describe('prefs', () => {
  it('defaults to microphone on, camera off, video received', () => {
    expect(loadPrefs(new MemoryStorage())).toEqual({ micOn: true, cameraOn: false, receiveVideo: true, cameraId: '', background: { kind: 'none' } });
  });
  it('round-trips', () => {
    const s = new MemoryStorage();
    savePrefs(s, { micOn: false, cameraOn: true, receiveVideo: false, cameraId: 'cam1' });
    expect(loadPrefs(s)).toEqual({ micOn: false, cameraOn: true, receiveVideo: false, cameraId: 'cam1', background: { kind: 'none' } });
  });
});

describe('noise filter preference', () => {
  it('keeps the noise filter choice, and leaves it unset when never made', () => {
    const m = new Map<string, string>();
    const store = { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v) };
    expect(loadPrefs(store).noiseFilter).toBeUndefined();
    savePrefs(store, { ...loadPrefs(store), noiseFilter: false });
    expect(loadPrefs(store).noiseFilter).toBe(false);
    m.set('kunveno.prefs', '{"noiseFilter":"yes"}');
    expect(loadPrefs(store).noiseFilter).toBeUndefined();
  });
});

describe('background preference', () => {
  it('reads a valid choice and drops a broken one', () => {
    const m = new Map<string, string>();
    const store = { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v) };
    expect(loadPrefs(store).background).toEqual({ kind: 'none' });
    savePrefs(store, { ...loadPrefs(store), background: { kind: 'blur' } });
    expect(loadPrefs(store).background).toEqual({ kind: 'blur' });
    m.set('kunveno.prefs', '{"background":{"kind":"sparkles"}}');
    expect(loadPrefs(store).background).toEqual({ kind: 'none' });
  });
});
