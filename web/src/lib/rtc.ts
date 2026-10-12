import type { ClientMessage } from './protocol';

export class Rtc {
  private readonly pc: RTCPeerConnection;
  private micSender: RTCRtpSender | null = null;
  private screenSender: RTCRtpSender | null = null;

  constructor(
    private readonly mic: MediaStreamTrack,
    private readonly send: (m: ClientMessage) => void,
    private readonly onTrack: (participantId: string, track: MediaStreamTrack) => void,
    private readonly onFailed: () => void,
    private readonly shareMaxKbps = 2500,
  ) {
    this.pc = new RTCPeerConnection({ iceServers: [] });
    this.pc.onicecandidate = (ev) => {
      if (ev.candidate) this.send({ type: 'candidate', candidate: ev.candidate.toJSON() });
    };
    this.pc.ontrack = (ev) => {
      const stream = ev.streams[0];
      this.onTrack(stream ? stream.id : '', ev.track);
    };
    this.pc.onconnectionstatechange = () => {
      if (this.pc.connectionState === 'failed') this.onFailed();
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
    if (this.micSender && this.screenSender) return;
    for (const t of this.pc.getTransceivers()) {
      if (t.mid === '0' && !this.micSender) {
        t.direction = 'sendonly';
        await t.sender.replaceTrack(this.mic);
        this.micSender = t.sender;
      } else if (t.mid === '1' && !this.screenSender) {
        t.direction = 'sendonly';
        this.screenSender = t.sender;
      }
    }
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

  async stopShare(): Promise<void> {
    await this.screenSender?.replaceTrack(null);
  }

  close(): void {
    this.pc.close();
  }
}
