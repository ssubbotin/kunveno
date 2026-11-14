// bot marks a server-side helper such as the echo; the parser always fills it in. guest marks a
// person who joined by an invitation link (sign-in on).
export type Quality = 'good' | 'fair' | 'poor';
export type BotKind = 'echo' | 'player' | 'camera' | 'notes' | 'recorder' | 'facilitator';
export type Participant = { id: string; name: string; muted: boolean; bot?: boolean; botKind?: BotKind; quality?: Quality; camera?: boolean; guest?: boolean };
const QUALITIES: readonly string[] = ['good', 'fair', 'poor'];
// A meeting document (documents.enabled): updated is Unix milliseconds, rev counts its versions.
export type DocumentItem = { id: string; title: string; updated: number; rev: number };
const DOCUMENT_ID = /^[a-z0-9-]{1,40}$/;

export type ServerMessage =
  | { type: 'joined'; self: Participant; room: { slug: string; name: string; facilitator?: boolean } }
  | { type: 'error'; code: string; message: string; retryAfter?: number }
  | { type: 'offer'; sdp: string }
  | { type: 'participants'; participants: Participant[]; sharer: string; echo: boolean; notes: boolean; recording: boolean; facilitator: boolean }
  | { type: 'reaction'; from: string; emoji: string }
  // The meeting's notes can be downloaded.
  | { type: 'notes_ready' }
  // The meeting's recording can be downloaded.
  | { type: 'recording_ready' }
  // notes.live: a recognised phrase; start is Unix milliseconds.
  | { type: 'phrase'; name: string; start: number; text: string }
  // The documents of the room's meeting (its start in Unix ms, as the meeting history names it), sent
  // when one is stored, replaced, deleted or expires, and on join.
  | { type: 'documents'; meeting: number; list: DocumentItem[] };

// RestartReason says why a page dropped its previous session; sent with the next join.
export type RestartReason = 'ws_closed' | 'media_disconnected' | 'media_failed' | 'connect_timeout' | 'media_error' | 'rtc_error';
export type Restart = { reason: RestartReason; after: number; transport: 'udp' | 'tcp' | '' };

export type ClientMessage =
  // A guest sends invite (the token from the link) in place of the PIN.
  | { type: 'join'; room: string; pin?: string; invite?: string; name: string; media?: 'tcp'; client?: string; restart?: Restart }
  | { type: 'answer'; sdp: string }
  | { type: 'candidate'; candidate: RTCIceCandidateInit }
  | { type: 'mute'; muted: boolean }
  | { type: 'share'; action: 'start' | 'stop' }
  | { type: 'echo'; on: boolean }
  | { type: 'notes'; on: boolean }
  | { type: 'recording'; on: boolean }
  | { type: 'facilitator'; on: boolean }
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

function documentItem(v: unknown): DocumentItem | null {
  const d = v as Partial<Record<keyof DocumentItem, unknown>> | null;
  if (typeof d !== 'object' || d === null) return null;
  const { id, title, updated, rev } = d;
  if (typeof id !== 'string' || !DOCUMENT_ID.test(id) || typeof title !== 'string' || title.trim() === '') return null;
  if (typeof updated !== 'number' || !Number.isFinite(updated) || typeof rev !== 'number' || !Number.isInteger(rev) || rev < 1) return null;
  return { id, title, updated, rev };
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
      return { type: 'joined', self: v.self, room: { slug: v.room.slug, name: v.room.name, ...(v.room.facilitator === true ? { facilitator: true } : {}) } };
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
          guest: p.guest === true ? true : undefined,
          botKind: ['echo', 'player', 'camera', 'notes', 'recorder', 'facilitator'].includes((p as { bot_kind?: unknown }).bot_kind as string)
            ? ((p as { bot_kind?: unknown }).bot_kind as BotKind) : undefined,
        })),
        sharer: v.sharer,
        echo: v.echo === true,
        notes: v.notes === true,
        recording: v.recording === true,
        facilitator: v.facilitator === true,
      };
    case 'reaction':
      if (typeof v.from !== 'string' || typeof v.emoji !== 'string') bad('reaction');
      return { type: 'reaction', from: v.from, emoji: v.emoji };
    case 'recording_ready':
      return { type: 'recording_ready' };
    case 'notes_ready':
      return { type: 'notes_ready' };
    case 'phrase':
      if (typeof v.name !== 'string' || typeof v.text !== 'string' || typeof v.start !== 'number' || !Number.isFinite(v.start)) bad('phrase');
      return { type: 'phrase', name: v.name, start: v.start, text: v.text };
    case 'documents':
      if (!Array.isArray(v.list) || !Number.isSafeInteger(v.meeting) || v.meeting <= 0) bad('documents');
      return { type: 'documents', meeting: v.meeting, list: v.list.map(documentItem).filter((d: DocumentItem | null): d is DocumentItem => d !== null) };
    default:
      return bad(`type ${v.type}`);
  }
}
