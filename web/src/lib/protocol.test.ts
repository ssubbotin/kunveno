import { describe, expect, it } from 'vitest';
import { parseServerMessage } from './protocol';

describe('parseServerMessage', () => {
  it('accepts every server message type', () => {
    expect(parseServerMessage('{"type":"joined","self":{"id":"a","name":"Ann","muted":false},"room":{"slug":"ops","name":"Operations"}}'))
      .toEqual({ type: 'joined', self: { id: 'a', name: 'Ann', muted: false }, room: { slug: 'ops', name: 'Operations' } });
    expect(parseServerMessage('{"type":"error","code":"bad_pin","message":"wrong pin"}'))
      .toEqual({ type: 'error', code: 'bad_pin', message: 'wrong pin' });
    expect(parseServerMessage('{"type":"offer","sdp":"v=0"}')).toEqual({ type: 'offer', sdp: 'v=0' });
    expect(parseServerMessage('{"type":"participants","participants":[{"id":"a","name":"Ann","muted":true}],"sharer":""}'))
      .toEqual({ type: 'participants', participants: [{ id: 'a', name: 'Ann', muted: true, bot: false }], sharer: '', echo: false, notes: false, recording: false, facilitator: false });
  });

  it('rejects junk', () => {
    for (const raw of ['nope', '{}', '{"type":"dance"}', '{"type":"offer"}', '{"type":"participants","participants":"x","sharer":""}']) {
      expect(() => parseServerMessage(raw)).toThrow(/bad message/);
    }
  });
  it('reads the echo flag and bot participants, defaulting both to false', () => {
    const m = parseServerMessage('{"type":"participants","participants":[{"id":"a","name":"Ann","muted":false},{"id":"e","name":"Echo","muted":false,"bot":true}],"sharer":"","echo":true}');
    expect(m).toEqual({ type: 'participants', participants: [{ id: 'a', name: 'Ann', muted: false, bot: false }, { id: 'e', name: 'Echo', muted: false, bot: true }], sharer: '', echo: true, notes: false, recording: false, facilitator: false });
    const old = parseServerMessage('{"type":"participants","participants":[],"sharer":""}');
    expect(old).toEqual({ type: 'participants', participants: [], sharer: '', echo: false, notes: false, recording: false, facilitator: false });
  });
  it('keeps known quality grades and drops anything else', () => {
    const m = parseServerMessage('{"type":"participants","participants":[{"id":"a","name":"Ann","muted":false,"quality":"poor"},{"id":"b","name":"Bob","muted":false,"quality":"excellent"},{"id":"c","name":"Cid","muted":false}],"sharer":""}');
    if (m.type !== 'participants') throw new Error('type');
    expect(m.participants.map((p) => p.quality)).toEqual(['poor', undefined, undefined]);
  });
  it('carries the wait of a too_many_attempts error', () => {
    expect(parseServerMessage('{"type":"error","code":"too_many_attempts","message":"x","retry_after":60}'))
      .toEqual({ type: 'error', code: 'too_many_attempts', message: 'x', retryAfter: 60 });
    expect(parseServerMessage('{"type":"error","code":"bad_pin","message":"x"}'))
      .toEqual({ type: 'error', code: 'bad_pin', message: 'x' });
  });
  it('reads which helper a bot is', () => {
    const m = parseServerMessage('{"type":"participants","participants":[{"id":"p","name":"Intro","muted":false,"bot":true,"bot_kind":"player"},{"id":"e","name":"x","muted":false,"bot":true,"bot_kind":"echo"},{"id":"b","name":"y","muted":false,"bot":true,"bot_kind":"other"},{"id":"c","name":"Двор","muted":false,"bot":true,"bot_kind":"camera"}],"sharer":""}');
    if (m.type !== 'participants') throw new Error('type');
    expect(m.participants.map((p) => p.botKind)).toEqual(['player', 'echo', undefined, 'camera']);
  });
  it('reads a reaction and refuses a malformed one', () => {
    expect(parseServerMessage('{"type":"reaction","from":"a","emoji":"🎉"}')).toEqual({ type: 'reaction', from: 'a', emoji: '🎉' });
    expect(() => parseServerMessage('{"type":"reaction","from":"a"}')).toThrow();
  });

  it('reads the notes flag, the notes bot, phrases and the ready notice', () => {
    const m = parseServerMessage('{"type":"participants","participants":[{"id":"n","name":"Заметки","muted":false,"bot":true,"bot_kind":"notes"}],"sharer":"","notes":true}');
    if (m.type !== 'participants') throw new Error('type');
    expect(m.notes).toBe(true);
    expect(m.participants[0].botKind).toBe('notes');
    expect(parseServerMessage('{"type":"notes_ready"}')).toEqual({ type: 'notes_ready' });
    expect(parseServerMessage('{"type":"phrase","name":"Анна","start":1760421420000,"text":"Начнём."}'))
      .toEqual({ type: 'phrase', name: 'Анна', start: 1760421420000, text: 'Начнём.' });
    for (const raw of ['{"type":"phrase","name":"Анна","start":1}', '{"type":"phrase","name":"Анна","start":"1","text":"x"}', '{"type":"phrase","start":1,"text":"x"}']) {
      expect(() => parseServerMessage(raw)).toThrow(/bad message/);
    }
  });
  it('parses facilitator state, offer and the facilitator bot', () => {
    const m = parseServerMessage(JSON.stringify({ type: 'participants', sharer: '', echo: false, notes: false, recording: false, facilitator: true,
      participants: [{ id: 'f', name: 'Ведущий', muted: false, bot: true, bot_kind: 'facilitator' }] }));
    expect(m).toMatchObject({ type: 'participants', facilitator: true, participants: [{ botKind: 'facilitator' }] });
    const j = parseServerMessage(JSON.stringify({ type: 'joined', self: { id: 'a', name: 'Ann', muted: false }, room: { slug: 'ops', name: 'Ops', facilitator: true } }));
    expect(j).toMatchObject({ type: 'joined', room: { facilitator: true } });
    const k = parseServerMessage(JSON.stringify({ type: 'joined', self: { id: 'a', name: 'Ann', muted: false }, room: { slug: 'ops', name: 'Ops' } }));
    expect(k).toEqual({ type: 'joined', self: { id: 'a', name: 'Ann', muted: false }, room: { slug: 'ops', name: 'Ops' } });
  });

  it('parses recording state, recording_ready and the recorder bot', () => {
    const m = parseServerMessage(JSON.stringify({ type: 'participants', sharer: '', echo: false, notes: false, recording: true,
      participants: [{ id: 'r', name: 'Запись', muted: false, bot: true, bot_kind: 'recorder' }] }));
    expect(m).toMatchObject({ type: 'participants', recording: true, participants: [{ botKind: 'recorder' }] });
    expect(parseServerMessage('{"type":"recording_ready"}')).toEqual({ type: 'recording_ready' });
  });
  it('marks guests and reads the sign-in error codes', () => {
    const m = parseServerMessage('{"type":"participants","participants":[{"id":"g","name":"Гость","muted":false,"guest":true},{"id":"a","name":"Ann","muted":false,"guest":"yes"}],"sharer":""}');
    if (m.type !== 'participants') throw new Error('type');
    expect(m.participants.map((p) => p.guest)).toEqual([true, undefined]);
    expect(parseServerMessage('{"type":"error","code":"auth_required","message":"x"}')).toEqual({ type: 'error', code: 'auth_required', message: 'x' });
    expect(parseServerMessage('{"type":"error","code":"bad_invite","message":"x"}')).toEqual({ type: 'error', code: 'bad_invite', message: 'x' });
  });
});
