import type { ServerMessage } from './protocol';
import { RoomState } from './state.svelte';
import { Signaling } from './signaling';
import { Rtc } from './rtc';
import { watchLevel } from './levels';

const FATAL_CODES = new Set(['bad_pin', 'bad_room', 'room_full', 'bad_message']);

type Wanted = { room: string; pin: string; name: string };

export class Session {
  readonly state = new RoomState();
  onScreen: (track: MediaStreamTrack | null) => void = () => {};

  private sig: Signaling | null = null;
  private rtc: Rtc | null = null;
  private mic: MediaStreamTrack | null = null;
  private wanted: Wanted | null = null;
  private attempt = 0;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private remoteAudio = new Map<string, { el: HTMLAudioElement; stop: () => void }>();
  private screenTrack: MediaStreamTrack | null = null;
  private screenAttached = false;
  private stopSelfLevel: (() => void) | null = null;

  async join(room: string, pin: string, name: string): Promise<void> {
    this.wanted = { room, pin, name };
    this.attempt = 0;
    if (!this.mic) {
      const stream = await navigator.mediaDevices.getUserMedia({
        audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true },
      });
      this.mic = stream.getAudioTracks()[0];
      this.mic.enabled = !this.state.muted;
      // Own level drives the speaking highlight of our row; a muted (disabled) track reads as silence.
      this.stopSelfLevel = watchLevel(this.mic, (speaking) => {
        if (this.state.selfId) this.state.speaking[this.state.selfId] = speaking;
      });
    }
    await this.connect();
  }

  private wsUrl(): string {
    return (location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/ws';
  }

  private async connect(): Promise<void> {
    if (!this.wanted || !this.mic) return;
    this.state.status = this.attempt === 0 ? 'connecting' : 'reconnecting';
    const sig = new Signaling(this.wsUrl(), (m) => this.onMessage(m), () => this.onClosed());
    this.sig = sig;
    try {
      await sig.connect();
    } catch {
      this.scheduleReconnect();
      return;
    }
    this.rtc = new Rtc(
      this.mic,
      (m) => sig.send(m),
      (pid, track) => this.onRemoteTrack(pid, track),
      () => sig.close(),
    );
    sig.send({ type: 'join', ...this.wanted });
  }

  private onMessage(m: ServerMessage): void {
    this.state.apply(m);
    switch (m.type) {
      case 'joined':
        this.attempt = 0;
        this.state.status = 'connected';
        if (this.state.muted) this.sig?.send({ type: 'mute', muted: true });
        break;
      case 'offer':
        void this.rtc?.handleOffer(m.sdp).catch((e) => console.error('offer', e));
        break;
      case 'participants':
        if (this.screenTrack && m.sharer === this.state.selfId && !this.screenAttached) {
          this.screenAttached = true;
          void this.rtc?.startShare(this.screenTrack);
          this.onScreen(this.screenTrack);
        } else if (this.screenTrack && m.sharer !== this.state.selfId) {
          this.dropScreen();
        }
        break;
      case 'error':
        if (m.code === 'share_busy') {
          this.dropScreen();
          this.state.status = 'connected';
        } else if (FATAL_CODES.has(m.code)) {
          this.wanted = null;
        }
        break;
    }
  }

  private onClosed(): void {
    this.teardownMedia();
    if (this.wanted) this.scheduleReconnect();
    else if (this.state.status !== 'error') this.state.status = 'idle';
  }

  private scheduleReconnect(): void {
    const delay = Math.min(10_000, 1000 * 2 ** this.attempt);
    this.attempt += 1;
    this.state.status = 'reconnecting';
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      if (this.wanted) void this.connect();
    }, delay);
  }

  leave(): void {
    this.wanted = null;
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer);
    this.sig?.send({ type: 'leave' });
    this.sig?.close();
    this.teardownMedia();
    this.stopSelfLevel?.();
    this.stopSelfLevel = null;
    this.mic?.stop();
    this.mic = null;
    this.state.reset();
    this.state.status = 'idle';
  }

  setMuted(muted: boolean): void {
    this.state.muted = muted;
    if (this.mic) this.mic.enabled = !muted;
    this.sig?.send({ type: 'mute', muted });
  }

  // Asks the server to bring the echo helper into this room, or to remove it.
  setEcho(on: boolean): void {
    this.sig?.send({ type: 'echo', on });
  }

  async startShare(): Promise<void> {
    if (this.screenTrack) return;
    let stream: MediaStream;
    try {
      stream = await navigator.mediaDevices.getDisplayMedia({ video: { frameRate: 15 }, audio: false });
    } catch {
      return; // user cancelled the picker
    }
    const track = stream.getVideoTracks()[0];
    this.screenTrack = track;
    track.onended = () => void this.stopShare();
    this.sig?.send({ type: 'share', action: 'start' });
  }

  async stopShare(): Promise<void> {
    if (!this.screenTrack) return;
    this.sig?.send({ type: 'share', action: 'stop' });
    this.dropScreen();
  }

  private dropScreen(): void {
    this.screenTrack?.stop();
    this.screenTrack = null;
    this.screenAttached = false;
    void this.rtc?.stopShare();
    this.onScreen(null);
  }

  private onRemoteTrack(pid: string, track: MediaStreamTrack): void {
    if (track.kind === 'audio') {
      const el = new Audio();
      el.srcObject = new MediaStream([track]);
      el.autoplay = true;
      void el.play().catch(() => {});
      const stop = watchLevel(track, (speaking) => {
        this.state.speaking[pid] = speaking;
      });
      this.remoteAudio.get(pid)?.stop();
      this.remoteAudio.set(pid, { el, stop });
      track.onended = () => {
        stop();
        el.pause();
        this.remoteAudio.delete(pid);
      };
      return;
    }
    this.onScreen(track);
    track.onmute = () => this.onScreen(null);
    track.onunmute = () => this.onScreen(track);
    track.onended = () => this.onScreen(null);
  }

  private teardownMedia(): void {
    this.rtc?.close();
    this.rtc = null;
    for (const { el, stop } of this.remoteAudio.values()) {
      stop();
      el.pause();
      el.srcObject = null;
    }
    this.remoteAudio.clear();
    if (this.screenTrack) this.dropScreen();
    this.state.speaking = {};
    this.state.participants = [];
    this.state.sharer = '';
  }
}
