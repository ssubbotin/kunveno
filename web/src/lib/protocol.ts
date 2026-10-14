// bot marks a server-side helper such as the echo; the parser always fills it in.
export type Quality = 'good' | 'fair' | 'poor';
export type BotKind = 'echo' | 'player' | 'camera' | 'notes';
export type Participant = { id: string; name: string; muted: boolean; bot?: boolean; botKind?: BotKind; quality?: Quality; camera?: boolean };
const QUALITIES: readonly string[] = ['good', 'fair', 'poor'];

export type ServerMessage =
  | { type: 'joined'; self: Participant; room: { slug: string; name: string } }
  | { type: 'error'; code: string; message: string; retryAfter?: number }
  | { type: 'offer'; sdp: string }
  | { type: 'participants'; participants: Participant[]; sharer: string; echo: boolean; notes: boolean }
  | { type: 'reaction'; from: string; emoji: string }
  // The meeting's notes can be downloaded.
  | { type: 'notes_ready' }
  // notes.live: a recognised phrase; start is Unix milliseconds.
  | { type: 'phrase'; name: string; start: number; text: string };

export type ClientMessage =
  | { type: 'join'; room: string; pin: string; name: string; media?: 'tcp'; client?: string }
  | { type: 'answer'; sdp: string }
  | { type: 'candidate'; candidate: RTCIceCandidateInit }
  | { type: 'mute'; muted: boolean }
  | { type: 'share'; action: 'start' | 'stop' }
  | { type: 'echo'; on: boolean }
  | { type: 'notes'; on: boolean }
  | { type: 'camera'; on: boolean }
  | { type: 'video'; receive: boolean }
  | { type: 'react'; emoji: string }
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
      return {
        type: 'error', code: v.code, message: typeof v.message === 'string' ? v.message : '',
        ...(typeof v.retry_after === 'number' && v.retry_after > 0 ? { retryAfter: v.retry_after } : {}),
      };
    case 'offer':
      if (typeof v.sdp !== 'string' || v.sdp === '') bad('offer');
      return { type: 'offer', sdp: v.sdp };
    case 'participants':
      if (!Array.isArray(v.participants) || !v.participants.every(isParticipant) || typeof v.sharer !== 'string') bad('participants');
      return {
        type: 'participants',
        participants: v.participants.map((p: Participant) => ({
          id: p.id, name: p.name, muted: p.muted, bot: p.bot === true,
          quality: QUALITIES.includes(p.quality as string) ? p.quality : undefined,
          camera: p.camera === true ? true : undefined,
          botKind: ['echo', 'player', 'camera', 'notes'].includes((p as { bot_kind?: unknown }).bot_kind as string)
            ? ((p as { bot_kind?: unknown }).bot_kind as BotKind) : undefined,
        })),
        sharer: v.sharer,
        echo: v.echo === true,
        notes: v.notes === true,
      };
    case 'reaction':
      if (typeof v.from !== 'string' || typeof v.emoji !== 'string') bad('reaction');
      return { type: 'reaction', from: v.from, emoji: v.emoji };
    case 'notes_ready':
      return { type: 'notes_ready' };
    case 'phrase':
      if (typeof v.name !== 'string' || typeof v.text !== 'string' || typeof v.start !== 'number' || !Number.isFinite(v.start)) bad('phrase');
      return { type: 'phrase', name: v.name, start: v.start, text: v.text };
    default:
      return bad(`type ${v.type}`);
  }
}
