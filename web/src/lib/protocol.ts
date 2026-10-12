export type Participant = { id: string; name: string; muted: boolean };

export type ServerMessage =
  | { type: 'joined'; self: Participant; room: { slug: string; name: string } }
  | { type: 'error'; code: string; message: string }
  | { type: 'offer'; sdp: string }
  | { type: 'participants'; participants: Participant[]; sharer: string };

export type ClientMessage =
  | { type: 'join'; room: string; pin: string; name: string }
  | { type: 'answer'; sdp: string }
  | { type: 'candidate'; candidate: RTCIceCandidateInit }
  | { type: 'mute'; muted: boolean }
  | { type: 'share'; action: 'start' | 'stop' }
  | { type: 'leave' };

function bad(what: string): never {
  throw new Error(`bad message: ${what}`);
}

function isParticipant(v: unknown): v is Participant {
  const p = v as Participant;
  return typeof p === 'object' && p !== null && typeof p.id === 'string' && typeof p.name === 'string' && typeof p.muted === 'boolean';
}

export function parseServerMessage(raw: string): ServerMessage {
  let v: any;
  try {
    v = JSON.parse(raw);
  } catch {
    bad('not json');
  }
  if (typeof v !== 'object' || v === null || typeof v.type !== 'string') bad('no type');
  switch (v.type) {
    case 'joined':
      if (!isParticipant(v.self) || typeof v.room?.slug !== 'string' || typeof v.room?.name !== 'string') bad('joined');
      return { type: 'joined', self: v.self, room: { slug: v.room.slug, name: v.room.name } };
    case 'error':
      if (typeof v.code !== 'string') bad('error');
      return { type: 'error', code: v.code, message: typeof v.message === 'string' ? v.message : '' };
    case 'offer':
      if (typeof v.sdp !== 'string' || v.sdp === '') bad('offer');
      return { type: 'offer', sdp: v.sdp };
    case 'participants':
      if (!Array.isArray(v.participants) || !v.participants.every(isParticipant) || typeof v.sharer !== 'string') bad('participants');
      return { type: 'participants', participants: v.participants, sharer: v.sharer };
    default:
      return bad(`type ${v.type}`);
  }
}
