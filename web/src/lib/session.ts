import type { ServerMessage } from './protocol';
import { RoomState } from './state.svelte';
import { Signaling } from './signaling';
import { Rtc } from './rtc';
import { watchLevel } from './levels';
import { MIC_KEY, audioConstraints } from './micLevel';
import { prefersTcp, rememberTcp } from './transport';
import { Reconnector, newClientId } from './reconnect';
import { forgetPin, rememberPin } from './pins';
import { cameraConstraints, loadPrefs, savePrefs, splitStream } from './media';

// How long a reaction stays on the sender's tile.
const REACTION_MS = 4000;

const FATAL_CODES = new Set(['bad_pin', 'bad_room', 'room_full', 'bad_message', 'too_many_attempts']);

type Wanted = { room: string; pin: string; name: string };

export class Session {
  readonly state = new RoomState();
  onScreen: (track: MediaStreamTrack | null) => void = () => {};

  private sig: Signaling | null = null;
  private rtc: Rtc | null = null;
  private mic: MediaStreamTrack | null = null;
  private wanted: Wanted | null = null;
  // The PIN the current room was entered with, for invitation links.
  get pin(): string {
    return this.wanted?.pin ?? '';
  }
  // One pending rejoin at most; see Reconnector.
  private readonly reconnector = new Reconnector(() => {
    if (this.wanted) void this.connect();
  });
  private everJoined = false;
  private readonly clientId = newClientId();
  private remoteAudio = new Map<string, { el: HTMLAudioElement; stop: () => void }>();
  private screenTrack: MediaStreamTrack | null = null;
  private screenAttached = false;
  private stopSelfLevel: (() => void) | null = null;
  private camera: MediaStreamTrack | null = null;
  // Set after our media connection failed, and remembered for a day; joins then ask the server
  // for ICE over TCP only, since a UDP path that broke once tends to break again (NAT or
  // firewall on a gateway).
  private preferTcp = prefersTcp(localStorage);

  async join(room: string, pin: string, name: string): Promise<void> {
    this.wanted = { room, pin, name };
    this.reconnector.cancel();
    this.reconnector.succeeded();
    this.everJoined = false;
    // Enter in the state chosen in the lobby.
    const prefs = loadPrefs(localStorage);
    this.state.muted = !prefs.micOn;
    this.state.receiveVideo = prefs.receiveVideo;
    if (this.mic) this.mic.enabled = prefs.micOn;
    this.state.hasCamera = await hasCamera();
    if (prefs.cameraOn && this.state.hasCamera && !this.camera) {
      try {
        this.camera = await openCamera(prefs.cameraId);
      } catch (e) {
        console.warn('camera', e); // join without it; the button stays available
      }
    }
    if (!this.mic) {
      const stream = await openMic(localStorage.getItem(MIC_KEY) ?? '');
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
    this.state.status = this.everJoined ? 'reconnecting' : 'connecting';
    // Events of a connection that is no longer the current one are ignored, so a late close
    // of an old socket cannot start a second session.
    const sig: Signaling = new Signaling(
      this.wsUrl(),
      (m) => { if (this.sig === sig) this.onMessage(m); },
      () => { if (this.sig === sig) this.onClosed(); },
    );
    this.sig = sig;
    try {
      await sig.connect();
    } catch {
      if (this.sig === sig) this.scheduleReconnect();
      return;
    }
    if (this.sig !== sig) {
      sig.close(); // superseded while connecting
      return;
    }
    this.rtc = new Rtc(
      this.mic,
      (m) => sig.send(m),
      (pid, track) => this.onRemoteTrack(pid, track),
      () => {
        this.useTcp();
        if (this.sig === sig) this.restart();
      },
      2500,
      (t) => (this.state.transport = t),
    );
    sig.send({ type: 'join', ...this.wanted, client: this.clientId, ...(this.preferTcp ? { media: 'tcp' as const } : {}) });
  }

  private onMessage(m: ServerMessage): void {
    this.state.apply(m);
    switch (m.type) {
      case 'joined':
        if (this.wanted) rememberPin(localStorage, this.wanted.room, this.wanted.pin); // accepted
        this.reconnector.succeeded();
        this.everJoined = true;
        this.state.status = 'connected';
        if (this.state.muted) this.sig?.send({ type: 'mute', muted: true });
        if (!this.state.receiveVideo) this.sig?.send({ type: 'video', receive: false });
        if (this.camera) void this.attachCamera();
        break;
      case 'reaction': {
        const r = this.state.reactions[m.from];
        if (r) setTimeout(() => this.state.clearReaction(m.from, r.seq), REACTION_MS);
        break;
      }
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
        } else if (m.code === 'media_failed') {
          this.useTcp();
        } else if (FATAL_CODES.has(m.code)) {
          if (m.code === 'bad_pin' && this.wanted) forgetPin(localStorage, this.wanted.room); // it changed
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
    this.state.status = 'reconnecting';
    this.reconnector.schedule();
  }

  // restart drops the current connection on purpose (media failed) and rejoins.
  private restart(): void {
    const sig = this.sig;
    this.sig = null;
    sig?.close();
    this.teardownMedia();
    if (this.wanted) this.scheduleReconnect();
  }


  leave(): void {
    this.wanted = null;
    this.reconnector.cancel();
    this.sig?.send({ type: 'leave' });
    this.sig?.close();
    this.teardownMedia();
    this.stopSelfLevel?.();
    this.stopSelfLevel = null;
    this.mic?.stop();
    this.mic = null;
    this.camera?.stop();
    this.camera = null;
    this.state.reset();
    this.state.status = 'idle';
  }

  setMuted(muted: boolean): void {
    this.state.muted = muted;
    if (this.mic) this.mic.enabled = !muted;
    this.sig?.send({ type: 'mute', muted });
  }

  private useTcp(): void {
    this.preferTcp = true;
    rememberTcp(localStorage);
  }

  // setCamera turns our camera on or off and remembers the choice for the next visit.
  async setCamera(on: boolean): Promise<void> {
    const prefs = loadPrefs(localStorage);
    savePrefs(localStorage, { ...prefs, cameraOn: on });
    if (!on) {
      this.dropCamera();
      this.sig?.send({ type: 'camera', on: false });
      return;
    }
    if (!this.camera) {
      try {
        this.camera = await openCamera(prefs.cameraId);
      } catch (e) {
        console.warn('camera', e);
        this.state.cameraError = 'camera.unavailable'; // a message key; the room translates it
        savePrefs(localStorage, { ...loadPrefs(localStorage), cameraOn: false });
        return;
      }
    }
    this.state.cameraError = '';
    await this.attachCamera();
  }

  // setReceiveVideo declines or accepts the other participants' cameras (audio and the shared
  // screen keep flowing), for a weak link.
  setReceiveVideo(on: boolean): void {
    this.state.receiveVideo = on;
    savePrefs(localStorage, { ...loadPrefs(localStorage), receiveVideo: on });
    this.sig?.send({ type: 'video', receive: on });
    if (!on) {
      const self = this.state.videos[this.state.selfId];
      this.state.videos = self ? { [this.state.selfId]: self } : {};
    }
  }

  private async attachCamera(): Promise<void> {
    const rtc = this.rtc;
    if (!this.camera || !rtc) return;
    await rtc.ready;
    if (rtc !== this.rtc || !this.camera) return; // reconnected or switched off meanwhile
    await rtc.startCamera(this.camera);
    this.state.cameraOn = true;
    this.state.videos = { ...this.state.videos, [this.state.selfId]: this.camera };
    this.sig?.send({ type: 'camera', on: true });
  }

  private dropCamera(): void {
    this.camera?.stop();
    this.camera = null;
    void this.rtc?.stopCamera();
    this.state.cameraOn = false;
    const { [this.state.selfId]: _, ...rest } = this.state.videos;
    this.state.videos = rest;
  }

  // react sends one of the fixed emoji; the server shows it to everyone, us included.
  react(emoji: string): void {
    this.sig?.send({ type: 'react', emoji });
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

  private onRemoteTrack(streamId: string, track: MediaStreamTrack): void {
    const { participant: pid, camera } = splitStream(streamId);
    if (camera) {
      this.state.videos = { ...this.state.videos, [pid]: track };
      track.onended = () => {
        const { [pid]: _, ...rest } = this.state.videos;
        this.state.videos = rest;
      };
      return;
    }
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
    this.state.videos = {};
    this.state.participants = [];
    this.state.sharer = '';
  }
}

// openMic opens the microphone chosen in the lobby, or the default one if it is gone.
async function openMic(deviceId: string): Promise<MediaStream> {
  try {
    return await navigator.mediaDevices.getUserMedia({ audio: audioConstraints(deviceId) });
  } catch (e) {
    if (!deviceId || (e as Error).name !== 'OverconstrainedError') throw e;
    return navigator.mediaDevices.getUserMedia({ audio: audioConstraints('') });
  }
}

// openCamera opens the camera chosen in the lobby, or the default one if it is gone.
async function openCamera(deviceId: string): Promise<MediaStreamTrack> {
  try {
    return (await navigator.mediaDevices.getUserMedia({ video: cameraConstraints(deviceId) })).getVideoTracks()[0];
  } catch (e) {
    if (!deviceId || (e as Error).name !== 'OverconstrainedError') throw e;
    return (await navigator.mediaDevices.getUserMedia({ video: cameraConstraints('') })).getVideoTracks()[0];
  }
}

async function hasCamera(): Promise<boolean> {
  try {
    return (await navigator.mediaDevices.enumerateDevices()).some((d) => d.kind === 'videoinput');
  } catch {
    return false;
  }
}
