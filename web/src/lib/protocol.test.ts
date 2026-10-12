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
      .toEqual({ type: 'participants', participants: [{ id: 'a', name: 'Ann', muted: true, bot: false }], sharer: '', echo: false });
  });

  it('rejects junk', () => {
    for (const raw of ['nope', '{}', '{"type":"dance"}', '{"type":"offer"}', '{"type":"participants","participants":"x","sharer":""}']) {
      expect(() => parseServerMessage(raw)).toThrow(/bad message/);
    }
  });
  it('reads the echo flag and bot participants, defaulting both to false', () => {
    const m = parseServerMessage('{"type":"participants","participants":[{"id":"a","name":"Ann","muted":false},{"id":"e","name":"Echo","muted":false,"bot":true}],"sharer":"","echo":true}');
    expect(m).toEqual({ type: 'participants', participants: [{ id: 'a', name: 'Ann', muted: false, bot: false }, { id: 'e', name: 'Echo', muted: false, bot: true }], sharer: '', echo: true });
    const old = parseServerMessage('{"type":"participants","participants":[],"sharer":""}');
    expect(old).toEqual({ type: 'participants', participants: [], sharer: '', echo: false });
  });
});
