import type { Participant, ServerMessage } from './protocol';

export type Status = 'idle' | 'connecting' | 'connected' | 'reconnecting' | 'error';

export class RoomState {
  status = $state<Status>('idle');
  // Our microphone's noise filter; set by the session, not by reset().
  noiseFilter = $state<'off' | 'on' | 'unavailable'>('off');
  errorCode = $state('');
  errorMessage = $state('');
  retryAfter = $state(0); // seconds, for too_many_attempts
  selfId = $state('');
  selfName = $state('');
  roomSlug = $state('');
  roomName = $state('');
  participants = $state<Participant[]>([]);
  sharer = $state('');
  muted = $state(false);
  speaking = $state<Record<string, boolean>>({});
  // Camera pictures by participant id, ours included; raw because tracks are not plain data.
  videos = $state.raw<Record<string, MediaStreamTrack>>({});
  cameraOn = $state(false);
  hasCamera = $state(true); // false on computers without any camera
  cameraError = $state('');
  // The camera background: what is chosen, the effect's frame rate, whether it kept the camera off
  // (it cannot start, or the stored image is gone), whether the stored image was gone at join
  // (until another choice), and whether an image could not replace the running effect. Not
  // cleared by reset().
  background = $state<'none' | 'blur' | 'image'>('none');
  backgroundFps = $state(0);
  backgroundError = $state(false);
  backgroundLost = $state(false);
  backgroundImageError = $state(false);
  receiveVideo = $state(true);
  echo = $state(false);
  // The emoji each participant sent last, shown on their tile for a few seconds.
  reactions = $state<Record<string, { emoji: string; seq: number }>>({});
  private reactionSeq = 0;
  transport = $state<'udp' | 'tcp' | ''>(''); // media path of this browser, once connected

  get isSharing(): boolean {
    return this.sharer !== '' && this.sharer === this.selfId;
  }

  get selfSpeaking(): boolean {
    return this.selfId !== '' && !!this.speaking[this.selfId];
  }

  // clearReaction removes a reaction when its time is up, unless a newer one replaced it.
  clearReaction(id: string, seq: number): void {
    if (this.reactions[id]?.seq === seq) delete this.reactions[id];
  }

  get peopleCount(): number {
    return this.participants.filter((p) => !p.bot).length;
  }

  get others(): Participant[] {
    return this.participants.filter((p) => p.id !== this.selfId);
  }

  reset(): void {
    this.selfId = '';
    this.errorCode = '';
    this.errorMessage = '';
    this.participants = [];
    this.sharer = '';
    this.speaking = {};
    this.videos = {};
    this.reactions = {};
    this.cameraOn = false;
    this.echo = false;
    this.transport = '';
  }

  apply(msg: ServerMessage): void {
    switch (msg.type) {
      case 'joined':
        this.selfId = msg.self.id;
        this.selfName = msg.self.name;
        this.roomSlug = msg.room.slug;
        this.roomName = msg.room.name;
        this.errorCode = '';
        this.errorMessage = '';
        break;
      case 'participants':
        this.participants = msg.participants;
        this.sharer = msg.sharer;
        this.echo = msg.echo;
        break;
      case 'error':
        this.status = 'error';
        this.errorCode = msg.code;
        this.errorMessage = msg.message;
        this.retryAfter = msg.retryAfter ?? 0;
        break;
      case 'reaction':
        this.reactions[msg.from] = { emoji: msg.emoji, seq: ++this.reactionSeq };
        break;
      case 'offer':
        break;
    }
  }
}
