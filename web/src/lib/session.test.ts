import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('./levels', () => ({ watchLevel: () => () => {} }));

// Microphone inputs the session opened, in order; the filter can be made to fail.
type FakeInput = { track: { enabled: boolean; stop(): void }; deviceId: string; filtered: boolean; stopped: boolean; stop(): void };
const mic = vi.hoisted(() => ({ inputs: [] as FakeInput[], filterWorks: true }));
vi.mock('./micInput', () => ({
  openMic: async (_id: string, filter: boolean) => {
    const i: FakeInput = {
      track: { enabled: true, stop() {} },
      deviceId: 'mic',
      filtered: filter && mic.filterWorks,
      stopped: false,
      stop() {
        i.stopped = true;
      },
    };
    mic.inputs.push(i);
    return i;
  },
}));

import { Session } from './session';
import { Rtc } from './rtc';
import { site } from './site.svelte';

// A stand-in for the browser WebSocket that records what the client sends.
class FakeSocket {
  static all: FakeSocket[] = [];
  static readonly OPEN = 1;
  readyState = 0;
  sent: { type: string }[] = [];
  closed = false;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onclose: (() => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  constructor(public url: string) {
    FakeSocket.all.push(this);
  }
  open() {
    this.readyState = FakeSocket.OPEN;
    this.onopen?.();
  }
  send(data: string) {
    this.sent.push(JSON.parse(data));
  }
  close() {
    this.closed = true;
    this.readyState = 3;
    this.onclose?.();
  }
}

// A peer connection that Chrome refuses to create while it believes the network is down.
let networkDown = false;
class FakePeerConnection {
  connectionState = 'new';
  onicecandidate = null;
  ontrack = null;
  onconnectionstatechange = null;
  constructor() {
    if (networkDown) throw new DOMException("Can't create RTCPeerConnections when the network is down", 'UnknownError');
  }
  close() {}
}

function memoryStorage(): Storage {
  const m = new Map<string, string>();
  return {
    getItem: (k) => m.get(k) ?? null,
    setItem: (k, v) => void m.set(k, v),
    removeItem: (k) => void m.delete(k),
    clear: () => m.clear(),
    key: (i) => [...m.keys()][i] ?? null,
    get length() {
      return m.size;
    },
  };
}

// Lets pending promise callbacks run.
const flush = () => new Promise<void>((r) => setTimeout(r, 0));

describe('Session', () => {
  beforeEach(() => {
    FakeSocket.all = [];
    networkDown = false;
    mic.inputs.length = 0;
    mic.filterWorks = true;
    const track = { enabled: true, stop() {} };
    vi.stubGlobal('WebSocket', FakeSocket);
    vi.stubGlobal('RTCPeerConnection', FakePeerConnection);
    vi.stubGlobal('localStorage', memoryStorage());
    vi.stubGlobal('location', { protocol: 'https:', host: 'meet.test' });
    vi.stubGlobal('navigator', {
      mediaDevices: {
        enumerateDevices: async () => [],
        getUserMedia: async () => ({ getAudioTracks: () => [track] }),
      },
    });
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  // joined enters a room and opens the socket, so the session has a peer connection.
  async function joined(): Promise<Session> {
    const s = new Session();
    const p = s.join('ops', '1234', 'Ann');
    await flush();
    FakeSocket.all[0].open();
    await p;
    return s;
  }

  it('retries the join when the peer connection cannot be created', async () => {
    const s = new Session();
    networkDown = true;
    const joining = s.join('ops', '1234', 'Ann');
    await flush();
    vi.useFakeTimers();
    FakeSocket.all[0].open();
    await expect(joining).resolves.toBeUndefined();
    // The socket that never got a join is closed at once instead of waiting for the server.
    expect(FakeSocket.all[0].closed).toBe(true);
    expect(FakeSocket.all[0].sent).toEqual([]);
    expect(s.state.status).toBe('reconnecting');

    networkDown = false;
    await vi.advanceTimersByTimeAsync(1000);
    expect(FakeSocket.all).toHaveLength(2);
    FakeSocket.all[1].open();
    await vi.advanceTimersByTimeAsync(0);
    expect(FakeSocket.all[1].sent.map((m) => m.type)).toEqual(['join']);
  });

  it('switches the sent microphone to the filtered one and keeps it muted', async () => {
    const replace = vi.spyOn(Rtc.prototype, 'replaceMic').mockResolvedValue();
    const s = await joined();
    s.setMuted(true);
    await s.setNoiseFilter(true);
    expect(replace).toHaveBeenCalledTimes(1);
    expect(replace).toHaveBeenCalledWith(mic.inputs[1].track);
    expect(mic.inputs[1].track.enabled).toBe(false);
    expect(mic.inputs[0].stopped).toBe(true);
    expect(s.state.noiseFilter).toBe('on');
    expect(JSON.parse(localStorage.getItem('kunveno.prefs') ?? '{}').noiseFilter).toBe(true);
  });

  it('says unavailable when the filter cannot start', async () => {
    vi.spyOn(Rtc.prototype, 'replaceMic').mockResolvedValue();
    const s = await joined();
    mic.filterWorks = false;
    await s.setNoiseFilter(true);
    expect(s.state.noiseFilter).toBe('unavailable');
  });

  it('ignores a second switch while one is in progress', async () => {
    const replace = vi.spyOn(Rtc.prototype, 'replaceMic').mockResolvedValue();
    const s = await joined();
    await Promise.all([s.setNoiseFilter(true), s.setNoiseFilter(false)]);
    expect(replace).toHaveBeenCalledTimes(1);
    expect(mic.inputs).toHaveLength(2);
    expect(JSON.parse(localStorage.getItem('kunveno.prefs') ?? '{}').noiseFilter).toBe(true);
  });

  it('stops the new input when the participant left during the switch', async () => {
    const replace = vi.spyOn(Rtc.prototype, 'replaceMic').mockResolvedValue();
    const s = await joined();
    const switching = s.setNoiseFilter(true);
    s.leave();
    await switching;
    expect(replace).not.toHaveBeenCalled();
    expect(mic.inputs[1].stopped).toBe(true);
  });

  it('opens the microphone at join with the site default when the participant never chose', async () => {
    site.noiseSuppression = true;
    try {
      const s = await joined();
      expect(s.state.noiseFilter).toBe('on');
    } finally {
      site.noiseSuppression = false;
    }
  });

  it('keeps the new microphone muted when mute is pressed while the track is being replaced', async () => {
    let release = () => {};
    vi.spyOn(Rtc.prototype, 'replaceMic').mockImplementation(() => new Promise<void>((r) => (release = r)));
    const s = await joined();
    const switching = s.setNoiseFilter(true);
    await flush();
    s.setMuted(true); // pressed while replaceTrack waits
    release();
    await switching;
    expect(mic.inputs[1].track.enabled).toBe(false);
  });

  it('stops the new input when the participant leaves while the track is being replaced', async () => {
    let fail = (_e: Error) => {};
    vi.spyOn(Rtc.prototype, 'replaceMic').mockImplementation(() => new Promise<void>((_r, j) => (fail = j)));
    const s = await joined();
    const switching = s.setNoiseFilter(true);
    await flush();
    s.leave();
    fail(new Error('closed')); // the connection is gone
    await switching;
    expect(mic.inputs[1].stopped).toBe(true);
  });

  it('stops the new input when leaving finishes first and the replacement then succeeds', async () => {
    let release = () => {};
    vi.spyOn(Rtc.prototype, 'replaceMic').mockImplementation(() => new Promise<void>((r) => (release = r)));
    const s = await joined();
    const switching = s.setNoiseFilter(true);
    await flush();
    s.leave();
    release();
    await switching;
    expect(mic.inputs[1].stopped).toBe(true);
  });

  it('keeps the microphone input across a reconnect', async () => {
    const s = await joined();
    vi.useFakeTimers();
    FakeSocket.all[0].close(); // the server went away
    await vi.advanceTimersByTimeAsync(1000);
    FakeSocket.all[1].open();
    await vi.advanceTimersByTimeAsync(0);
    expect(mic.inputs).toHaveLength(1);
    expect(mic.inputs[0].stopped).toBe(false);
    expect(s.state.status).not.toBe('idle');
  });
});

