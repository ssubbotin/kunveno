import type { Restart, RestartReason, ServerMessage } from './protocol';
import { RoomState } from './state.svelte';
import { Signaling } from './signaling';
import { Rtc } from './rtc';
import { watchLevel } from './levels';
import { MIC_KEY, noiseFilterOn } from './micLevel';
import { openMic, type MicInput } from './micInput';
import { site } from './site.svelte';
import { forgetTcp, prefersTcp, rememberTcp } from './transport';
import { Reconnector, newClientId } from './reconnect';
import { forgetPin, rememberPin } from './pins';
import { loadPrefs, savePrefs, splitStream } from './media';
import { BackgroundUnavailable, modeFor, openCamera, type CameraInput } from './cameraInput';
import { NONE, sameEffect, type BackgroundChoice } from './background/choice';
import { isLost } from './background/stored';

// How long a reaction stays on the sender's tile.
const REACTION_MS = 4000;

const FATAL_CODES = new Set(['bad_pin', 'bad_room', 'room_full', 'bad_message', 'too_many_attempts']);

type Wanted = { room: string; pin: string; name: string };

export class Session {
  readonly state = new RoomState();
  onScreen: (track: MediaStreamTrack | null) => void = () => {};

  private sig: Signaling | null = null;
  private rtc: Rtc | null = null;
  private micIn: MicInput | null = null;
  private switchingMic = false;
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
  // Why the last session was dropped on our side, until the next join reports it to the server;
  // the first reason wins (a media failure is followed by the socket closing). Null after the
  // person's own join or leave.
  private dropped: { reason: RestartReason; after: number; transport: 'udp' | 'tcp' | '' } | null = null;
  private connectStarted = 0;
  private lastTransport: 'udp' | 'tcp' | '' = '';
  private readonly clientId = newClientId();
  private remoteAudio = new Map<string, { el: HTMLAudioElement; stop: () => void }>();
  private screenTrack: MediaStreamTrack | null = null;
  private screenAttached = false;
  private stopSelfLevel: (() => void) | null = null;
  private cam: CameraInput | null = null;
  // Set while the camera opens or switches; a background change meanwhile is ignored.
  private switchingCam = false;
  // Counts camera switch-offs and leaves, so an open that finishes after one is thrown away.
  private camEpoch = 0;
  // The chosen camera background, from the preferences.
  private background: BackgroundChoice = NONE;
  // Set when the stored image was gone at join: the camera stays off until another choice.
  private lost = false;
  // Set after our media connection failed, and remembered for a week; joins then ask the server
  // for ICE over TCP only, since a UDP path that broke once tends to break again (NAT or
  // firewall on a gateway). The participant can try UDP again (tryFastChannel).
  private preferTcp = prefersTcp(localStorage);

  constructor() {
    this.state.tcpForced = this.preferTcp;
    this.useBackground(loadPrefs(localStorage).background ?? NONE);
  }

  private useBackground(choice: BackgroundChoice): void {
    this.background = choice;
    this.state.background = choice.kind;
  }

  async join(room: string, pin: string, name: string): Promise<void> {
    this.wanted = { room, pin, name };
    this.reconnector.cancel();
    this.reconnector.succeeded();
    this.everJoined = false;
    this.dropped = null;
    // Enter in the state chosen in the lobby.
    const prefs = loadPrefs(localStorage);
    this.state.muted = !prefs.micOn;
    this.state.receiveVideo = prefs.receiveVideo;
    if (this.micIn) this.micIn.track.enabled = prefs.micOn;
    this.state.hasCamera = await hasCamera();
    await this.loadBackground();
    if (prefs.cameraOn && this.state.hasCamera && !this.cam) {
      try {
        this.cam = await this.openFreshCam();
      } catch (e) {
        console.warn('camera', e); // join without it; the button stays available
      }
    }
    if (!this.micIn) {
      const filter = noiseFilterOn(prefs.noiseFilter, site.noiseSuppression);
      this.micIn = await openMic(localStorage.getItem(MIC_KEY) ?? '', filter);
      this.micIn.track.enabled = !this.state.muted;
      this.state.noiseFilter = filterState(filter, this.micIn);
      this.watchSelf();
    }
    await this.connect();
  }

  // loadBackground takes the stored choice. An image that is gone (removed from the server, or the
  // own image deleted) stays chosen but keeps the camera off until the person chooses again, so the
  // room is never shown by accident; the picker and the camera button say so.
  private async loadBackground(): Promise<void> {
    const stored = loadPrefs(localStorage).background ?? NONE;
    this.useBackground(stored);
    this.lost = await isLost(stored);
    this.state.backgroundLost = this.lost;
  }

  private onFps = (fps: number) => (this.state.backgroundFps = fps);

  // openCam opens the camera with the current background; when the background cannot start it
  // reports so and leaves the camera off. Other camera errors are thrown.
  private async openCam(reuse?: CameraInput): Promise<CameraInput | null> {
    if (this.lost) {
      this.state.backgroundError = true; // the dialog says the image is gone
      return null;
    }
    try {
      const c = await openCamera(loadPrefs(localStorage).cameraId, this.background, this.onFps, reuse && { raw: reuse.raw, deviceId: reuse.deviceId });
      this.state.backgroundError = false;
      return c;
    } catch (e) {
      if (e instanceof BackgroundUnavailable) {
        console.warn('background', e);
        this.state.backgroundError = true;
        return null;
      }
      throw e;
    }
  }

  // openFreshCam opens the camera for setCamera and join. The open includes the effect's worker and
  // model, which take seconds: a background change meanwhile is ignored, and an input that finishes
  // opening after the camera was switched off or the session left is stopped.
  private async openFreshCam(): Promise<CameraInput | null> {
    const epoch = this.camEpoch;
    this.switchingCam = true;
    try {
      const c = await this.openCam();
      if (c && this.camEpoch !== epoch) {
        c.stop();
        return null;
      }
      return c;
    } finally {
      this.switchingCam = false;
    }
  }

  // setBackground remembers the choice; with the camera open it switches inside the effect, or
  // reopens the camera when the effect starts or stops. A call during a switch is ignored, the
  // stored choice included, so the choice and the camera agree.
  async setBackground(choice: BackgroundChoice): Promise<void> {
    if (this.switchingCam) return;
    savePrefs(localStorage, { ...loadPrefs(localStorage), background: choice });
    const before = this.background;
    this.useBackground(choice);
    this.lost = false;
    this.state.backgroundLost = false;
    this.state.backgroundImageError = false;
    if (!this.cam) return;
    this.switchingCam = true;
    try {
      if (this.cam.effect && sameEffect(before, choice)) {
        let mode;
        try {
          mode = await modeFor(choice);
        } catch (e) {
          if (!(e instanceof BackgroundUnavailable)) throw e;
          // The image cannot load; the running effect stays as it was, and the picker says so.
          // The camera works, so this is no reason to offer it without a background.
          console.warn('background', e);
          this.state.backgroundImageError = true;
          this.useBackground(before);
          return;
        }
        if (mode && this.cam?.effect) this.cam.effect.setMode(mode);
        return;
      }
      let next: CameraInput | null;
      try {
        next = await this.openCam(this.cam); // the open camera, with or without the effect on top
      } catch (e) {
        this.useBackground(before); // the running camera stays
        throw e;
      }
      if (!next) {
        this.useBackground(before); // keep what is running
        return;
      }
      if (!this.cam) {
        next.stop(); // switched off or left meanwhile
        return;
      }
      // The new input is ours from here on, so switching off and leave act on it while the track
      // is replaced.
      const old = this.cam;
      const rtc = this.rtc;
      const sending = this.state.cameraOn;
      this.cam = next;
      this.showOwnCamera();
      try {
        if (sending) await rtc?.replaceCamera(next.track);
      } catch (e) {
        // A reconnect or leave closes the connection under us; on the same connection the old
        // camera stays.
        if (this.cam === next && this.rtc === rtc && rtc) {
          this.cam = old;
          this.useBackground(before);
          this.showOwnCamera();
          next.release(); // the camera stays with the old input
          throw e;
        }
      }
      old.release(); // the camera itself now belongs to next
      if (this.cam !== next) next.stop(); // left meanwhile
    } finally {
      this.switchingCam = false;
    }
  }

  // cameraWithoutBackground answers the "background unavailable" or "background gone" notice:
  // camera on, no background.
  async cameraWithoutBackground(): Promise<void> {
    this.state.backgroundError = false;
    await this.setBackground(NONE);
    await this.setCamera(true);
  }

  private showOwnCamera(): void {
    if (this.cam && this.state.cameraOn) this.state.videos = { ...this.state.videos, [this.state.selfId]: this.cam.track };
  }

  // Own level drives the speaking highlight of our row; a muted (disabled) track reads as silence.
  private watchSelf(): void {
    this.stopSelfLevel?.();
    this.stopSelfLevel = null;
    if (!this.micIn) return;
    this.stopSelfLevel = watchLevel(this.micIn.track, (speaking) => {
      if (this.state.selfId) this.state.speaking[this.state.selfId] = speaking;
    });
  }

  // setNoiseFilter remembers the choice and, in a call, reopens the microphone with or without the
  // filter and sends the new track in place of the old one. A call while a switch is under way is
  // ignored, the stored choice included, so the switch, the choice and the microphone agree.
  async setNoiseFilter(on: boolean): Promise<void> {
    if (this.switchingMic) return;
    savePrefs(localStorage, { ...loadPrefs(localStorage), noiseFilter: on });
    if (!this.micIn) return;
    this.switchingMic = true;
    try {
      const next = await openMic(localStorage.getItem(MIC_KEY) ?? '', on);
      if (!this.micIn) {
        next.stop(); // left meanwhile
        return;
      }
      next.track.enabled = !this.state.muted;
      // The new input is ours from here on, so mute and leave act on it while the track is replaced.
      const old = this.micIn;
      const rtc = this.rtc;
      this.micIn = next;
      this.watchSelf();
      try {
        await rtc?.replaceMic(next.track);
      } catch (e) {
        // A reconnect or leave closes the connection under us; a new one will send next. On the same
        // connection the old microphone stays.
        if (this.micIn === next && this.rtc === rtc && rtc) {
          this.micIn = old;
          old.track.enabled = !this.state.muted;
          this.watchSelf();
          next.stop();
          throw e;
        }
      }
      old.stop();
      if (this.micIn !== next) {
        next.stop(); // left meanwhile
        return;
      }
      this.state.noiseFilter = filterState(on, next);
    } finally {
      this.switchingMic = false;
    }
  }

  private wsUrl(): string {
    return (location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/ws';
  }

  private async connect(): Promise<void> {
    if (!this.wanted || !this.micIn) return;
    this.state.status = this.everJoined ? 'reconnecting' : 'connecting';
    this.connectStarted = Date.now();
    this.lastTransport = '';
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
    try {
      this.rtc = new Rtc(
        this.micIn.track,
        (m) => sig.send(m),
        (pid, track) => this.onRemoteTrack(pid, track),
        (reason) => {
          // pc.close() fires no state change, so a closed connection's watch can still go off.
          if (this.sig !== sig) return;
          this.noteDrop(reason);
          this.useTcp();
          this.restart();
        },
        2500,
        (t) => (this.state.transport = this.lastTransport = t),
        site.reconnectGrace * 1000,
      );
    } catch (e) {
      // Chrome refuses to create a peer connection while it believes the network is down, which
      // happens for a moment when the network changes. Retry instead of leaving the socket open
      // without a join until the server gives up on it.
      console.warn('rtc', e);
      this.noteDrop('rtc_error');
      this.restart();
      return;
    }
    const restart: Restart | undefined = this.dropped ?? undefined;
    this.dropped = null;
    sig.send({ type: 'join', ...this.wanted, client: this.clientId, ...(this.preferTcp ? { media: 'tcp' as const } : {}), ...(restart ? { restart } : {}) });
  }

  // noteDrop remembers why our session ended, for the next join. The first reason stays.
  private noteDrop(reason: RestartReason): void {
    this.dropped ??= { reason, after: Math.max(0, Math.round((Date.now() - this.connectStarted) / 1000)), transport: this.lastTransport };
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
        if (this.cam) void this.attachCamera();
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
          this.noteDrop('media_error');
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
    if (this.wanted) this.noteDrop('ws_closed');
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
    this.dropped = null;
    this.reconnector.cancel();
    this.sig?.send({ type: 'leave' });
    this.sig?.close();
    this.teardownMedia();
    this.stopSelfLevel?.();
    this.stopSelfLevel = null;
    this.micIn?.stop();
    this.micIn = null;
    this.state.noiseFilter = 'off';
    this.camEpoch++;
    this.cam?.stop();
    this.cam = null;
    this.state.backgroundFps = 0;
    this.state.reset();
    this.state.status = 'idle';
  }

  setMuted(muted: boolean): void {
    this.state.muted = muted;
    if (this.micIn) this.micIn.track.enabled = !muted;
    this.sig?.send({ type: 'mute', muted });
  }

  private useTcp(): void {
    this.preferTcp = true;
    this.state.tcpForced = true;
    rememberTcp(localStorage);
  }

  // tryFastChannel rejoins over UDP after an earlier failure sent this browser to TCP. If UDP
  // fails again, the connection watch falls back to TCP as before.
  tryFastChannel(): void {
    if (!this.preferTcp) return;
    this.preferTcp = false;
    this.state.tcpForced = false;
    forgetTcp(localStorage);
    this.restart();
  }

  // setCamera turns our camera on or off and remembers the choice for the next visit.
  async setCamera(on: boolean): Promise<void> {
    if (on && !this.cam && this.switchingCam) return; // already opening
    const prefs = loadPrefs(localStorage);
    savePrefs(localStorage, { ...prefs, cameraOn: on });
    if (!on) {
      this.state.cameraStarting = false; // a click while it starts cancels the start
      this.dropCamera();
      this.sig?.send({ type: 'camera', on: false });
      return;
    }
    const startEpoch = this.camEpoch;
    this.state.cameraStarting = true;
    try {
      await this.startCamera();
    } finally {
      if (this.camEpoch === startEpoch) this.state.cameraStarting = false;
    }
  }

  // startCamera opens the camera (with the background, if one is chosen) and sends it.
  private async startCamera(): Promise<void> {
    if (!this.cam) {
      const epoch = this.camEpoch;
      let opened: CameraInput | null;
      try {
        opened = await this.openFreshCam();
      } catch (e) {
        console.warn('camera', e);
        // A message key; the room translates it. Not shown when the camera was switched off or the
        // session left meanwhile.
        if (this.camEpoch === epoch) this.state.cameraError = 'camera.unavailable';
        opened = null;
      }
      if (this.camEpoch !== epoch) return; // switched off or left meanwhile; the input is stopped
      if (!opened) {
        // No camera, or its background cannot start or is gone (state.backgroundError): it stays off.
        savePrefs(localStorage, { ...loadPrefs(localStorage), cameraOn: false });
        return;
      }
      this.cam = opened;
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
    if (!this.cam || !rtc) return;
    await rtc.ready;
    if (rtc !== this.rtc || !this.cam) return; // reconnected or switched off meanwhile
    await rtc.startCamera(this.cam.track);
    this.state.cameraOn = true;
    this.showOwnCamera();
    this.sig?.send({ type: 'camera', on: true });
  }

  private dropCamera(): void {
    this.camEpoch++;
    this.cam?.stop();
    this.cam = null;
    this.state.backgroundFps = 0;
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

  // Asks the server to take meeting notes in this room, or to stop.
  setNotes(on: boolean): void {
    this.sig?.send({ type: 'notes', on });
  }

  // Asks the server to record the meeting in this room, or to stop.
  setRecording(on: boolean): void {
    this.sig?.send({ type: 'recording', on });
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

// filterState is what the switch shows: off, on, or asked for but not running in this browser.
function filterState(asked: boolean, input: MicInput): 'off' | 'on' | 'unavailable' {
  return !asked ? 'off' : input.filtered ? 'on' : 'unavailable';
}

async function hasCamera(): Promise<boolean> {
  try {
    return (await navigator.mediaDevices.enumerateDevices()).some((d) => d.kind === 'videoinput');
  } catch {
    return false;
  }
}
