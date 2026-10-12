import type { Participant, ServerMessage } from './protocol';

export type Status = 'idle' | 'connecting' | 'connected' | 'reconnecting' | 'error';

export class RoomState {
  status = $state<Status>('idle');
  errorCode = $state('');
  errorMessage = $state('');
  selfId = $state('');
  selfName = $state('');
  roomSlug = $state('');
  roomName = $state('');
  participants = $state<Participant[]>([]);
  sharer = $state('');
  muted = $state(false);
  speaking = $state<Record<string, boolean>>({});

  get isSharing(): boolean {
    return this.sharer !== '' && this.sharer === this.selfId;
  }

  get selfSpeaking(): boolean {
    return this.selfId !== '' && !!this.speaking[this.selfId];
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
        break;
      case 'error':
        this.status = 'error';
        this.errorCode = msg.code;
        this.errorMessage = msg.message;
        break;
      case 'offer':
        break;
    }
  }
}
