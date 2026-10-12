import { describe, expect, it } from 'vitest';
import { RoomState } from './state.svelte';

describe('RoomState', () => {
  it('tracks self, participants and sharer', () => {
    const s = new RoomState();
    s.apply({ type: 'joined', self: { id: 'a', name: 'Ann', muted: false }, room: { slug: 'ops', name: 'Operations' } });
    expect(s.selfId).toBe('a');
    expect(s.roomName).toBe('Operations');
    s.apply({ type: 'participants', participants: [{ id: 'a', name: 'Ann', muted: false }, { id: 'b', name: 'Bob', muted: true }], sharer: 'b' });
    expect(s.others.map((p) => p.id)).toEqual(['b']);
    expect(s.isSharing).toBe(false);
    s.apply({ type: 'participants', participants: [{ id: 'a', name: 'Ann', muted: false }], sharer: 'a' });
    expect(s.isSharing).toBe(true);
  });

  it('records errors', () => {
    const s = new RoomState();
    s.apply({ type: 'error', code: 'room_full', message: 'full' });
    expect(s.status).toBe('error');
    expect(s.errorCode).toBe('room_full');
  });
});
