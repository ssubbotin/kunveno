import { CAMERA_MAX_KBPS } from './media';
import type { ClientMessage } from './protocol';
import { transportFromStats, watchConnection, type FailReason } from './transport';

export class Rtc {
  private readonly pc: RTCPeerConnection;
  private micSender: RTCRtpSender | null = null;
  private screenSender: RTCRtpSender | null = null;
  private cameraSender: RTCRtpSender | null = null;
  private resolveReady: () => void = () => {};
  // ready resolves once the send slots are bound, after the first offer.
  readonly ready = new Promise<void>((r) => (this.resolveReady = r));

  constructor(
    private mic: MediaStreamTrack,
    private readonly send: (m: ClientMessage) => void,
    private readonly onTrack: (participantId: string, track: MediaStreamTrack) => void,
    private readonly onFailed: (reason: FailReason) => void,
    private readonly shareMaxKbps = 2500,
    private readonly onTransport: (t: 'udp' | 'tcp' | '') => void = () => {},
    graceMs?: number,
  ) {
    this.pc = new RTCPeerConnection({ iceServers: [] });
    this.pc.onicecandidate = (ev) => {
      // Skip the end-of-candidates marker (empty string); the server needs only real candidates.
      if (ev.candidate?.candidate) this.send({ type: 'candidate', candidate: ev.candidate.toJSON() });
    };
    this.pc.ontrack = (ev) => {
      const stream = ev.streams[0];
      this.onTrack(stream ? stream.id : '', ev.track);
    };
    const watch = watchConnection((reason) => this.onFailed(reason), graceMs);
    watch(this.pc.connectionState); // "new": starts the window for the connection to come up
    this.pc.onconnectionstatechange = () => {
      watch(this.pc.connectionState);
      if (this.pc.connectionState === 'connected') {
        void this.pc.getStats().then((r) => this.onTransport(transportFromStats(r.values() as Iterable<{ id: string; type: string }>)));
      }
    };
  }

  async handleOffer(sdp: string): Promise<void> {
    await this.pc.setRemoteDescription({ type: 'offer', sdp });
    await this.ensureSendSlots();
    const answer = await this.pc.createAnswer();
    await this.pc.setLocalDescription(answer);
    this.send({ type: 'answer', sdp: answer.sdp ?? '' });
  }

  // The server's first two transceivers (mid 0 audio, mid 1 video) are our send slots.
  private async ensureSendSlots(): Promise<void> {
    if (this.micSender && this.screenSender && this.cameraSender) return;
    for (const t of this.pc.getTransceivers()) {
      if (t.mid === '0' && !this.micSender) {
        t.direction = 'sendonly';
        await t.sender.replaceTrack(this.mic);
        this.micSender = t.sender;
      } else if (t.mid === '1' && !this.screenSender) {
        t.direction = 'sendonly';
        this.screenSender = t.sender;
      } else if (t.mid === '2' && !this.cameraSender) {
        t.direction = 'sendonly';
        this.cameraSender = t.sender;
      }
    }
    if (this.micSender && this.screenSender && this.cameraSender) this.resolveReady();
  }

  // replaceMic sends another microphone track (the noise filter turned on or off); before the
  // first offer the slot is bound with it then.
  async replaceMic(track: MediaStreamTrack): Promise<void> {
    this.mic = track;
    await this.micSender?.replaceTrack(track);
  }

  async startShare(track: MediaStreamTrack): Promise<void> {
    if (!this.screenSender) throw new Error('not negotiated yet');
    track.contentHint = 'detail';
    await this.screenSender.replaceTrack(track);
    const params = this.screenSender.getParameters();
    if (!params.encodings || params.encodings.length === 0) params.encodings = [{}];
    params.encodings[0].maxBitrate = this.shareMaxKbps * 1000;
    (params as RTCRtpSendParameters & { degradationPreference?: string }).degradationPreference = 'maintain-resolution';
    try {
      await this.screenSender.setParameters(params);
    } catch (e) {
      console.warn('setParameters', e);
    }
  }

  // The camera slot never renegotiates: turning it on or off swaps the track.
  async startCamera(track: MediaStreamTrack): Promise<void> {
    if (!this.cameraSender) throw new Error('not negotiated yet');
    await this.cameraSender.replaceTrack(track);
    const params = this.cameraSender.getParameters();
    if (!params.encodings || params.encodings.length === 0) params.encodings = [{}];
    params.encodings[0].maxBitrate = CAMERA_MAX_KBPS * 1000;
    try {
      await this.cameraSender.setParameters(params);
    } catch (e) {
      console.warn('camera setParameters', e);
    }
  }

  // replaceCamera sends another camera track (a background turned on or off) on the camera slot.
  async replaceCamera(track: MediaStreamTrack): Promise<void> {
    await this.cameraSender?.replaceTrack(track);
  }

  async stopCamera(): Promise<void> {
    await this.cameraSender?.replaceTrack(null);
  }

  async stopShare(): Promise<void> {
    await this.screenSender?.replaceTrack(null);
  }

  close(): void {
    this.pc.close();
  }
}
