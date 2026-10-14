import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('./compositor', () => ({ Compositor: class { dispose() {} } }));
vi.mock('./segmenter', () => ({ createSegmenter: async () => { throw new Error('no WebGL'); } }));

// The worker's init can fail (no WebGL, the model refusing to load); the camera frames handed to it
// must not be left in the transferred stream, unclosed.
describe('background worker', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('cancels the frame stream when init fails', async () => {
    const posted: { type: string }[] = [];
    const scope: { onmessage: ((e: { data: unknown }) => Promise<void>) | null } = { onmessage: null };
    vi.stubGlobal('self', scope);
    vi.stubGlobal('postMessage', (m: { type: string }) => posted.push(m));
    await import('./worker');
    const cancel = vi.fn(async () => {});
    await scope.onmessage!({ data: { type: 'init', canvas: {}, width: 640, height: 360, frames: { cancel } } });
    expect(posted.map((m) => m.type)).toEqual(['error']);
    expect(cancel).toHaveBeenCalledTimes(1);
  });
});
