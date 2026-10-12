import { afterEach, describe, expect, it, vi } from 'vitest';
import { Signaling } from './signaling';

// A stand-in for the browser WebSocket that lets the test fire its events.
class FakeSocket {
  static last: FakeSocket;
  readyState = 0;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onclose: (() => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  constructor(public url: string) { FakeSocket.last = this; }
  send() {}
  close() { this.onclose?.(); }
}

describe('Signaling', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('reports a failed connect once, by rejecting, and does not also call onClose', async () => {
    vi.stubGlobal('WebSocket', FakeSocket);
    const onClose = vi.fn();
    const sig = new Signaling('ws://x/ws', () => {}, onClose);
    const p = sig.connect();
    FakeSocket.last.onerror?.();
    FakeSocket.last.onclose?.(); // browsers fire both when the server is down
    await expect(p).rejects.toThrow();
    expect(onClose).not.toHaveBeenCalled();
  });

  it('calls onClose when an open connection ends', async () => {
    vi.stubGlobal('WebSocket', FakeSocket);
    const onClose = vi.fn();
    const sig = new Signaling('ws://x/ws', () => {}, onClose);
    const p = sig.connect();
    FakeSocket.last.onopen?.();
    await p;
    FakeSocket.last.onclose?.();
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
