import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('./levels', () => ({ watchLevel: () => () => {} }));

import { Session } from './session';

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
  });

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
});
