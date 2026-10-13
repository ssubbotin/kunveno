import { describe, expect, it } from 'vitest';
import { RoomState } from './state.svelte';

describe('RoomState', () => {
  it('tracks self, participants and sharer', () => {
    const s = new RoomState();
    s.apply({ type: 'joined', self: { id: 'a', name: 'Ann', muted: false }, room: { slug: 'ops', name: 'Operations' } });
    expect(s.selfId).toBe('a');
    expect(s.roomName).toBe('Operations');
    s.apply({ type: 'participants', participants: [{ id: 'a', name: 'Ann', muted: false }, { id: 'b', name: 'Bob', muted: true }], sharer: 'b', echo: false });
    expect(s.others.map((p) => p.id)).toEqual(['b']);
    expect(s.isSharing).toBe(false);
    s.apply({ type: 'participants', participants: [{ id: 'a', name: 'Ann', muted: false }], sharer: 'a', echo: false });
    expect(s.isSharing).toBe(true);
  });

  it('records errors', () => {
    const s = new RoomState();
    s.apply({ type: 'error', code: 'room_full', message: 'full' });
    expect(s.status).toBe('error');
    expect(s.errorCode).toBe('room_full');
  });
  it('reset clears a previous error so the lobby starts clean', () => {
    const s = new RoomState();
    s.apply({ type: 'error', code: 'bad_message', message: 'x' });
    s.reset();
    expect(s.errorCode).toBe('');
    expect(s.errorMessage).toBe('');
  });
  it('selfSpeaking follows the speaking flag of the own participant id', () => {
    const s = new RoomState();
    s.speaking[''] = true;
    expect(s.selfSpeaking).toBe(false);
    s.apply({ type: 'joined', self: { id: 'a', name: 'Ann', muted: false }, room: { slug: 'ops', name: 'Operations' } });
    s.speaking.a = true;
    expect(s.selfSpeaking).toBe(true);
  });
  it('tracks the echo flag and keeps bots out of the people count', () => {
    const s = new RoomState();
    s.apply({ type: 'participants', participants: [{ id: 'a', name: 'Ann', muted: false, bot: false }, { id: 'e', name: 'Echo', muted: false, bot: true }], sharer: '', echo: true });
    expect(s.echo).toBe(true);
    expect(s.peopleCount).toBe(1);
    s.reset();
    expect(s.echo).toBe(false);
  });
  it('shows the latest reaction per participant and lets only its own timer clear it', () => {
    const s = new RoomState();
    s.apply({ type: 'reaction', from: 'a', emoji: '👍' });
    const first = s.reactions.a.seq;
    s.apply({ type: 'reaction', from: 'a', emoji: '🎉' });
    expect(s.reactions.a.emoji).toBe('🎉');
    s.clearReaction('a', first); // the first one's timer fires late
    expect(s.reactions.a?.emoji).toBe('🎉');
    s.clearReaction('a', s.reactions.a.seq);
    expect(s.reactions.a).toBeUndefined();
  });
});
