import { afterEach, describe, expect, it, vi } from 'vitest';
import { BackgroundUnavailable, startEffect } from './effect';

// A failure before the worker reports ready must not leave the worker (with MediaPipe loaded)
// running, nor the camera attached to the hidden video.
describe('startEffect', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('terminates the worker and detaches the video when the video cannot play', async () => {
    const workers: { terminated: boolean }[] = [];
    class FakeWorker {
      terminated = false;
      onmessage: unknown = null;
      onerror: unknown = null;
      constructor() {
        workers.push(this);
      }
      postMessage() {}
      terminate() {
        this.terminated = true;
      }
    }
    const video = {
      muted: false,
      playsInline: false,
      srcObject: null as unknown,
      play: () => Promise.reject(new Error('play refused')),
    };
    const canvas = { width: 0, height: 0, getContext: () => ({ transferFromImageBitmap() {} }) };
    vi.stubGlobal('Worker', FakeWorker);
    vi.stubGlobal('MediaStream', class {});
    vi.stubGlobal('document', { createElement: (tag: string) => (tag === 'video' ? video : canvas) });
    const raw = { getSettings: () => ({ width: 640, height: 360 }) } as unknown as MediaStreamTrack;

    await expect(startEffect(raw, { kind: 'blur' })).rejects.toBeInstanceOf(BackgroundUnavailable);
    expect(workers).toHaveLength(1);
    expect(workers[0].terminated).toBe(true);
    expect(video.srcObject).toBeNull();
  });
});
