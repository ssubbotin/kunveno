import { describe, expect, it } from 'vitest';
import { MAX_PHRASES, RoomState } from './state.svelte';

describe('RoomState', () => {
  it('tracks self, participants and sharer', () => {
    const s = new RoomState();
    s.apply({ type: 'joined', self: { id: 'a', name: 'Ann', muted: false }, room: { slug: 'ops', name: 'Operations' } });
    expect(s.selfId).toBe('a');
    expect(s.roomName).toBe('Operations');
    s.apply({ type: 'participants', participants: [{ id: 'a', name: 'Ann', muted: false }, { id: 'b', name: 'Bob', muted: true }], sharer: 'b', echo: false, notes: false, recording: false });
    expect(s.others.map((p) => p.id)).toEqual(['b']);
    expect(s.isSharing).toBe(false);
    s.apply({ type: 'participants', participants: [{ id: 'a', name: 'Ann', muted: false }], sharer: 'a', echo: false, notes: false, recording: false });
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
    s.apply({ type: 'participants', participants: [{ id: 'a', name: 'Ann', muted: false, bot: false }, { id: 'e', name: 'Echo', muted: false, bot: true }], sharer: '', echo: true, notes: false, recording: false });
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

  it('follows the notes flag, the ready notice and live phrases', () => {
    const s = new RoomState();
    s.apply({ type: 'notes_ready' });
    expect(s.notesReady).toBe(true);
    s.apply({ type: 'participants', participants: [], sharer: '', echo: false, notes: true, recording: false });
    expect(s.notes).toBe(true);
    expect(s.notesReady).toBe(false); // a new recording hides the old notice
    for (let i = 0; i < MAX_PHRASES + 5; i++) s.apply({ type: 'phrase', name: 'Анна', start: i, text: `фраза ${i}` });
    expect(s.phrases.length).toBe(MAX_PHRASES);
    expect(s.phrases.at(-1)?.text).toBe(`фраза ${MAX_PHRASES + 4}`);
    expect(new Set(s.phrases.map((p) => p.seq)).size).toBe(MAX_PHRASES);
    s.reset();
    expect([s.notes, s.notesReady, s.phrases.length]).toEqual([false, false, 0]);
  });
  it('tracks recording and recording_ready', () => {
    const s = new RoomState();
    s.apply({ type: 'participants', participants: [], sharer: '', echo: false, notes: false, recording: true });
    expect(s.recording).toBe(true);
    s.apply({ type: 'recording_ready' });
    expect(s.recordingReady).toBe(true);
    s.reset();
    expect(s.recording).toBe(false);
    expect(s.recordingReady).toBe(false);
  });
});
