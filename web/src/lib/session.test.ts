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

// Camera inputs the session opened, in order; the background can be made to fail.
type FakeRaw = { id: string; stopped: boolean; stop(): void };
type FakeCam = { track: { id: string; stop(): void }; raw: FakeRaw; deviceId: string; effect: { mode: unknown; setMode(m: unknown): void; stop(): void } | null; stopped: boolean; released: boolean; stop(): void; release(): void };
// gate holds an open until released (worker start-up and model load take seconds); fail makes the
// next open reject with a plain error (the device busy).
// imageFails makes an image unloadable while blur still works.
const cam = vi.hoisted(() => ({ opened: [] as FakeCam[], captures: 0, unavailable: false, imageFails: false, gate: null as Promise<void> | null, fail: null as Error | null }));
// Whether this browser holds an own image: null when its storage cannot be read.
const own = vi.hoisted(() => ({ has: true as boolean | null }));
vi.mock('./background/ownImage', () => ({ hasOwn: async () => own.has }));
vi.mock('./cameraInput', () => {
  class BackgroundUnavailable extends Error {}
  return {
    BackgroundUnavailable,
    modeFor: async (c: { kind: string }) => {
      if (c.kind === 'image' && cam.imageFails) throw new BackgroundUnavailable('image');
      return c.kind === 'none' ? null : { kind: c.kind };
    },
    openCamera: async (_id: string, choice: { kind: string }, _onStats?: unknown, reuse?: { raw: FakeRaw }) => {
      if (cam.gate) await cam.gate;
      if (cam.fail) throw cam.fail;
      if (choice.kind !== 'none' && cam.unavailable) throw new BackgroundUnavailable('no');
      if (!reuse) cam.captures++;
      const raw: FakeRaw = reuse?.raw ?? { id: `raw${cam.captures}`, stopped: false, stop() { raw.stopped = true; } };
      const c: FakeCam = {
        track: { id: `cam${cam.opened.length}`, stop() {} },
        raw,
        deviceId: 'cam',
        effect: choice.kind === 'none' ? null : { mode: { kind: choice.kind }, setMode(m) { this.mode = m; }, stop() {} },
        stopped: false,
        released: false,
        stop() {
          c.stopped = true;
          raw.stop();
        },
        release() {
          c.released = true;
        },
      };
      cam.opened.push(c);
      return c;
    },
  };
});

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
  // Enough of an offer/answer for the send slots to bind, so rtc.ready resolves.
  async setRemoteDescription() {}
  async createAnswer() {
    return { type: 'answer', sdp: 'v=0' };
  }
  async setLocalDescription() {}
  private transceivers = ['0', '1', '2'].map((mid) => ({ mid, direction: 'recvonly', sender: { replaceTrack: async () => {} } }));
  getTransceivers() {
    return this.transceivers;
  }
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
    cam.opened.length = 0;
    cam.captures = 0;
    cam.unavailable = false;
    cam.imageFails = false;
    own.has = true;
    cam.gate = null;
    cam.fail = null;
    const track = { enabled: true, stop() {} };
    vi.stubGlobal('WebSocket', FakeSocket);
    vi.stubGlobal('RTCPeerConnection', FakePeerConnection);
    vi.stubGlobal('localStorage', memoryStorage());
    vi.stubGlobal('location', { protocol: 'https:', host: 'meet.test' });
    vi.stubGlobal('navigator', {
      mediaDevices: {
        enumerateDevices: async () => [{ kind: 'videoinput', deviceId: 'cam' }],
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
    // The server's first offer binds the send slots.
    FakeSocket.all[0].onmessage?.({ data: JSON.stringify({ type: 'offer', sdp: 'v=0' }) });
    await flush();
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

  it('falls back to TCP after a media failure and tries UDP again on request', async () => {
    const s = await joined();
    vi.useFakeTimers();
    expect(s.state.tcpForced).toBe(false);
    FakeSocket.all[0].onmessage?.({ data: JSON.stringify({ type: 'error', code: 'media_failed', message: 'x' }) });
    FakeSocket.all[0].close();
    await vi.advanceTimersByTimeAsync(1000);
    FakeSocket.all[1].open();
    await vi.advanceTimersByTimeAsync(0);
    expect(FakeSocket.all[1].sent[0]).toMatchObject({ type: 'join', media: 'tcp' });
    expect(s.state.tcpForced).toBe(true);
    expect(localStorage.getItem('kunveno.tcpUntil')).not.toBeNull();

    s.tryFastChannel();
    expect(FakeSocket.all[1].closed).toBe(true);
    expect(s.state.tcpForced).toBe(false);
    expect(localStorage.getItem('kunveno.tcpUntil')).toBeNull();
    await vi.advanceTimersByTimeAsync(10_000); // no "joined" came, so the backoff grew
    FakeSocket.all[2].open();
    await vi.advanceTimersByTimeAsync(0);
    expect(FakeSocket.all[2].sent[0]).toMatchObject({ type: 'join' });
    expect(FakeSocket.all[2].sent[0]).not.toHaveProperty('media');
    // A browser already on UDP has nothing to retry.
    s.tryFastChannel();
    expect(FakeSocket.all[2].closed).toBe(false);
  });

  it('starts forced to TCP when a failure is remembered', () => {
    localStorage.setItem('kunveno.tcpUntil', String(Date.now() + 60_000));
    expect(new Session().state.tcpForced).toBe(true);
  });

  it('switches blur to an image inside the effect, without reopening the camera', async () => {
    vi.spyOn(Rtc.prototype, 'startCamera').mockResolvedValue();
    const replace = vi.spyOn(Rtc.prototype, 'replaceCamera').mockResolvedValue();
    const s = await joined();
    await s.setBackground({ kind: 'blur' });
    await s.setCamera(true);
    await s.setBackground({ kind: 'image', source: 'own' });
    expect(cam.opened).toHaveLength(1);
    expect(replace).not.toHaveBeenCalled();
    expect(s.state.background).toBe('image');
  });

  it('switches blur, glass blocks and an image inside the effect, without reopening the camera', async () => {
    vi.spyOn(Rtc.prototype, 'startCamera').mockResolvedValue();
    const replace = vi.spyOn(Rtc.prototype, 'replaceCamera').mockResolvedValue();
    const s = await joined();
    await s.setBackground({ kind: 'blur' });
    await s.setCamera(true);
    await s.setBackground({ kind: 'glass' });
    expect(s.state.background).toBe('glass');
    await s.setBackground({ kind: 'image', source: 'own' });
    expect(cam.opened).toHaveLength(1);
    expect(replace).not.toHaveBeenCalled();
    expect(s.state.background).toBe('image');
  });

  it('switches between none and a background on the camera already open, without opening it again', async () => {
    vi.spyOn(Rtc.prototype, 'startCamera').mockResolvedValue();
    const replace = vi.spyOn(Rtc.prototype, 'replaceCamera').mockResolvedValue();
    const s = await joined();
    await s.setCamera(true);
    await s.setBackground({ kind: 'blur' });
    expect(cam.captures).toBe(1); // the device is opened once; a second open of a USB camera takes seconds
    expect(cam.opened[1].raw).toBe(cam.opened[0].raw);
    expect(replace).toHaveBeenCalledTimes(1);
    expect(cam.opened[0].released).toBe(true);
    expect(cam.opened[0].raw.stopped).toBe(false);
    await s.setBackground({ kind: 'none' });
    expect(cam.captures).toBe(1);
    expect(cam.opened[1].released).toBe(true);
    expect(cam.opened[2].raw.stopped).toBe(false);
    s.leave();
    expect(cam.opened[2].raw.stopped).toBe(true);
  });

  it('keeps the camera off when the background cannot start', async () => {
    const start = vi.spyOn(Rtc.prototype, 'startCamera').mockResolvedValue();
    const s = await joined();
    cam.unavailable = true;
    await s.setBackground({ kind: 'blur' });
    await s.setCamera(true);
    expect(start).not.toHaveBeenCalled();
    expect(s.state.cameraOn).toBe(false);
    expect(s.state.backgroundError).toBe(true);
    await s.cameraWithoutBackground();
    expect(start).toHaveBeenCalledTimes(1);
    expect(s.state.background).toBe('none');
    expect(s.state.backgroundError).toBe(false);
  });

  it('only remembers the choice while the camera is off', async () => {
    const s = await joined();
    await s.setBackground({ kind: 'blur' });
    expect(cam.opened).toHaveLength(0);
    expect(JSON.parse(localStorage.getItem('kunveno.prefs') ?? '{}').background).toEqual({ kind: 'blur' });
  });

  it('stops the new camera input when leaving during a switch', async () => {
    vi.spyOn(Rtc.prototype, 'startCamera').mockResolvedValue();
    let release = () => {};
    vi.spyOn(Rtc.prototype, 'replaceCamera').mockImplementation(() => new Promise<void>((r) => (release = r)));
    const s = await joined();
    await s.setCamera(true);
    const switching = s.setBackground({ kind: 'blur' });
    await flush();
    s.leave();
    release();
    await switching;
    expect(cam.opened[1].stopped).toBe(true);
    // The old input gave the camera to the new one; nothing of it is left running.
    expect(cam.opened[0].released).toBe(true);
    expect(cam.opened[0].raw.stopped).toBe(true);
  });

  it('keeps a stored blur at join, where only images can be lost', async () => {
    localStorage.setItem('kunveno.prefs', JSON.stringify({ cameraOn: true, background: { kind: 'blur' } }));
    const s = await joined();
    expect(s.state.background).toBe('blur');
    expect(s.state.backgroundLost).toBe(false);
    expect(cam.opened).toHaveLength(1);
    expect(cam.opened[0].effect).not.toBeNull();
  });

  const presets = (...names: string[]) => async () => ({ ok: true, json: async () => names.map((name) => ({ name, width: 1920, height: 1080 })) });
  const prefs = () => JSON.parse(localStorage.getItem('kunveno.prefs') ?? '{}');

  it('keeps the camera off at join when the stored server image is gone, and says so at once', async () => {
    const start = vi.spyOn(Rtc.prototype, 'startCamera').mockResolvedValue();
    localStorage.setItem('kunveno.prefs', JSON.stringify({ cameraOn: true, background: { kind: 'image', source: 'server', name: 'gone.jpg' } }));
    vi.stubGlobal('fetch', presets('beach.jpg'));
    const s = await joined();
    expect(cam.opened).toHaveLength(0);
    expect(s.state.cameraOn).toBe(false);
    expect(s.state.backgroundLost).toBe(true);
    expect(s.state.backgroundError).toBe(true); // the dialog opens at once
    // Pressing the camera button again does not show the room either.
    s.state.backgroundError = false; // the dialog was cancelled
    await s.setCamera(true);
    expect(cam.opened).toHaveLength(0);
    expect(s.state.backgroundError).toBe(true);
    expect(start).not.toHaveBeenCalled();
    // Only the explicit answer turns the camera on without a background.
    await s.cameraWithoutBackground();
    expect(cam.opened).toHaveLength(1);
    expect(cam.opened[0].effect).toBeNull();
    expect(s.state.cameraOn).toBe(true);
    expect(s.state.backgroundLost).toBe(false);
    expect(prefs().background).toEqual({ kind: 'none' });
  });

  it('keeps the camera off at join when the own image was deleted', async () => {
    localStorage.setItem('kunveno.prefs', JSON.stringify({ cameraOn: true, background: { kind: 'image', source: 'own' } }));
    own.has = false;
    const s = await joined();
    expect(cam.opened).toHaveLength(0);
    expect(s.state.backgroundLost).toBe(true);
    expect(s.state.backgroundError).toBe(true);
  });

  it('a chosen background ends the lost state', async () => {
    vi.spyOn(Rtc.prototype, 'startCamera').mockResolvedValue();
    localStorage.setItem('kunveno.prefs', JSON.stringify({ cameraOn: false, background: { kind: 'image', source: 'server', name: 'gone.jpg' } }));
    vi.stubGlobal('fetch', presets());
    const s = await joined();
    expect(s.state.backgroundLost).toBe(true);
    expect(s.state.backgroundError).toBe(false); // the camera was not wanted: nothing to say yet
    await s.setBackground({ kind: 'blur' });
    expect(s.state.backgroundLost).toBe(false);
    await s.setCamera(true);
    expect(cam.opened).toHaveLength(1);
    expect(cam.opened[0].effect).not.toBeNull();
  });

  it('keeps the stored image when the presets cannot be fetched', async () => {
    localStorage.setItem('kunveno.prefs', JSON.stringify({ cameraOn: true, background: { kind: 'image', source: 'server', name: 'beach.jpg' } }));
    vi.stubGlobal('fetch', async () => ({ ok: false, status: 502, json: async () => [] }));
    const s = await joined();
    expect(s.state.backgroundLost).toBe(false);
    expect(s.state.background).toBe('image');
    expect(prefs().background).toEqual({ kind: 'image', source: 'server', name: 'beach.jpg' });
    expect(cam.opened).toHaveLength(1);
    expect(cam.opened[0].effect).not.toBeNull();
  });

  it('keeps the stored own image when this browser storage cannot be read', async () => {
    localStorage.setItem('kunveno.prefs', JSON.stringify({ cameraOn: true, background: { kind: 'image', source: 'own' } }));
    own.has = null;
    const s = await joined();
    expect(s.state.backgroundLost).toBe(false);
    expect(cam.opened).toHaveLength(1);
    expect(cam.opened[0].effect).not.toBeNull();
  });

  it('keeps blur running and reports in the picker when an image cannot load', async () => {
    vi.spyOn(Rtc.prototype, 'startCamera').mockResolvedValue();
    const s = await joined();
    await s.setBackground({ kind: 'blur' });
    await s.setCamera(true);
    cam.imageFails = true;
    await s.setBackground({ kind: 'image', source: 'server', name: 'beach.jpg' });
    expect(s.state.backgroundError).toBe(false); // no dialog that offers the camera without a background
    expect(s.state.backgroundImageError).toBe(true);
    expect(s.state.background).toBe('blur');
    expect(cam.opened[0].effect?.mode).toEqual({ kind: 'blur' });
    expect(s.state.cameraOn).toBe(true);
    cam.imageFails = false;
    await s.setBackground({ kind: 'image', source: 'own' });
    expect(s.state.backgroundImageError).toBe(false);
  });

  // slowOpen makes the next camera opens wait until the returned function is called.
  function slowOpen(): () => void {
    let release = () => {};
    cam.gate = new Promise<void>((r) => (release = r));
    return () => {
      cam.gate = null;
      release();
    };
  }

  it('stops a camera that finished opening after the participant left', async () => {
    const start = vi.spyOn(Rtc.prototype, 'startCamera').mockResolvedValue();
    const s = await joined();
    const release = slowOpen();
    const turning = s.setCamera(true);
    await flush();
    s.leave();
    release();
    await turning;
    expect(cam.opened).toHaveLength(1);
    expect(cam.opened[0].stopped).toBe(true);
    expect(start).not.toHaveBeenCalled();
  });

  it('stops a camera that finished opening after it was switched off', async () => {
    const start = vi.spyOn(Rtc.prototype, 'startCamera').mockResolvedValue();
    const s = await joined();
    const release = slowOpen();
    const turning = s.setCamera(true);
    await flush();
    await s.setCamera(false);
    release();
    await turning;
    expect(cam.opened[0].stopped).toBe(true);
    expect(start).not.toHaveBeenCalled();
    expect(s.state.cameraOn).toBe(false);
  });

  it('stops a camera opened at join when the participant left meanwhile', async () => {
    localStorage.setItem('kunveno.prefs', JSON.stringify({ cameraOn: true }));
    const release = slowOpen();
    const s = new Session();
    const joining = s.join('ops', '1234', 'Ann');
    await flush();
    s.leave();
    release();
    await joining;
    expect(cam.opened).toHaveLength(1);
    expect(cam.opened[0].stopped).toBe(true);
  });

  it('ignores a background change while the camera is opening', async () => {
    vi.spyOn(Rtc.prototype, 'startCamera').mockResolvedValue();
    const s = await joined();
    const release = slowOpen();
    const turning = s.setCamera(true);
    await flush();
    await s.setBackground({ kind: 'blur' });
    release();
    await turning;
    expect(s.state.cameraOn).toBe(true);
    expect(cam.opened).toHaveLength(1);
    expect(cam.opened[0].effect).toBeNull();
    expect(s.state.background).toBe('none');
    expect(JSON.parse(localStorage.getItem('kunveno.prefs') ?? '{}').background).toEqual({ kind: 'none' });
  });

  it('keeps the running camera and its background when reopening fails', async () => {
    vi.spyOn(Rtc.prototype, 'startCamera').mockResolvedValue();
    const replace = vi.spyOn(Rtc.prototype, 'replaceCamera').mockResolvedValue();
    const s = await joined();
    await s.setCamera(true);
    cam.fail = new Error('NotReadableError');
    await expect(s.setBackground({ kind: 'blur' })).rejects.toThrow('NotReadableError');
    expect(s.state.background).toBe('none');
    expect(cam.opened[0].stopped).toBe(false);
    expect(replace).not.toHaveBeenCalled();
    expect(s.state.cameraOn).toBe(true);
  });

  it('shows the camera as starting from the click until it is sent, and a second click cancels', async () => {
    vi.spyOn(Rtc.prototype, 'startCamera').mockResolvedValue();
    const s = await joined();
    let open = () => {};
    cam.gate = new Promise<void>((r) => (open = r));
    const starting = s.setCamera(true);
    await flush();
    expect(s.state.cameraStarting).toBe(true);
    await s.setCamera(false); // the second click, while the effect is still loading
    expect(s.state.cameraStarting).toBe(false);
    open();
    await starting;
    expect(s.state.cameraOn).toBe(false);
    expect(s.state.cameraStarting).toBe(false);
    expect(cam.opened.every((c) => c.stopped)).toBe(true);
    cam.gate = null;
    await s.setCamera(true);
    expect(s.state.cameraOn).toBe(true);
    expect(s.state.cameraStarting).toBe(false);
  });
});
