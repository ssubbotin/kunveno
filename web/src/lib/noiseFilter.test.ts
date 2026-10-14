import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const lib = vi.hoisted(() => ({ binary: new ArrayBuffer(0) }));
vi.mock('@sapphi-red/web-noise-suppressor', () => ({
  loadRnnoise: async () => lib.binary,
  RnnoiseWorkletNode: class {
    connect() {}
    disconnect() {}
    destroy() {}
  },
}));

import { filterTrack } from './noiseFilter';

// The smallest valid WebAssembly module: the magic number and version.
const EMPTY_MODULE = new Uint8Array([0, 97, 115, 109, 1, 0, 0, 0]).buffer;

// An AudioContext stand-in that records resumes and closes and can change state.
class FakeContext {
  static last: FakeContext;
  state = 'running';
  resumed = 0;
  closed = false;
  onstatechange: (() => void) | null = null;
  audioWorklet = { addModule: async () => {} };
  constructor() {
    FakeContext.last = this;
  }
  createMediaStreamSource() {
    return { connect() {}, disconnect() {} };
  }
  createMediaStreamDestination() {
    const track = { stop() {} };
    return { stream: { getAudioTracks: () => [track] } };
  }
  async resume() {
    this.resumed += 1;
  }
  async close() {
    this.closed = true;
  }
  become(state: string) {
    this.state = state;
    this.onstatechange?.();
  }
}

describe('filterTrack', () => {
  beforeEach(() => {
    vi.stubGlobal('AudioContext', FakeContext);
    vi.stubGlobal('AudioWorkletNode', class {});
    vi.stubGlobal('MediaStream', class {});
    vi.stubGlobal('addEventListener', () => {});
    vi.stubGlobal('removeEventListener', () => {});
  });
  afterEach(() => vi.unstubAllGlobals());

  it('fails when the WebAssembly file is something else, such as an HTML page', async () => {
    lib.binary = new TextEncoder().encode('<!doctype html><title>kunveno</title>').buffer;
    await expect(filterTrack({} as MediaStreamTrack)).rejects.toThrow();
    expect(FakeContext.last.closed).toBe(true);
  });

  it('resumes the context when the browser suspends it', async () => {
    lib.binary = EMPTY_MODULE;
    const f = await filterTrack({} as MediaStreamTrack);
    const before = FakeContext.last.resumed;
    FakeContext.last.become('suspended');
    expect(FakeContext.last.resumed).toBe(before + 1);
    f.stop();
    FakeContext.last.become('closed');
    expect(FakeContext.last.resumed).toBe(before + 1);
  });
});
