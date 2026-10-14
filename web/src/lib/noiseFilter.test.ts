import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const lib = vi.hoisted(() => ({ binary: new ArrayBuffer(0), node: null as Record<string, unknown> | null }));
vi.mock('@sapphi-red/web-noise-suppressor', () => ({
  loadGtcrn: async () => lib.binary,
  GtcrnWorkletNode: class {
    channelCount = 2;
    channelCountMode = 'max';
    channelInterpretation = 'speakers';
    constructor() {
      lib.node = this as unknown as Record<string, unknown>;
    }
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
  dest: Record<string, unknown> | null = null;
  createMediaStreamDestination() {
    const track = { stop() {} };
    this.dest = { channelCount: 2, channelCountMode: 'explicit', stream: { getAudioTracks: () => [track] } };
    return this.dest;
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

  it('keeps the path mono, so the voice is not on one side only', async () => {
    lib.binary = EMPTY_MODULE;
    await filterTrack({} as MediaStreamTrack);
    expect(lib.node).toMatchObject({ channelCount: 1, channelCountMode: 'explicit' });
    expect(FakeContext.last.dest).toMatchObject({ channelCount: 1, channelCountMode: 'explicit' });
  });
});
