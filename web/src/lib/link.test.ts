import { describe, expect, it } from 'vitest';
import { parseJoinLink, roomLink } from './link';

describe('room links', () => {
  it('puts the room in the path and the PIN in the fragment', () => {
    expect(roomLink('https://meet.example', 'ops', '391722')).toBe('https://meet.example/join/ops#pin=391722');
  });

  it('keeps a non-default port and encodes the PIN', () => {
    expect(roomLink('https://10.0.0.5:8443', 'ops', 'a b#&')).toBe('https://10.0.0.5:8443/join/ops#pin=a%20b%23%26');
  });

  it('reads the room and the PIN back', () => {
    expect(parseJoinLink('/join/ops', '#pin=391722')).toEqual({ room: 'ops', pin: '391722' });
    expect(parseJoinLink('/join/ops', '#pin=a%20b%23%26')).toEqual({ room: 'ops', pin: 'a b#&' });
  });

  it('accepts a link without a PIN', () => {
    expect(parseJoinLink('/join/ops', '')).toEqual({ room: 'ops', pin: '' });
    expect(parseJoinLink('/join/ops/', '#')).toEqual({ room: 'ops', pin: '' });
  });

  it('ignores anything that is not a join link', () => {
    expect(parseJoinLink('/', '')).toBeNull();
    expect(parseJoinLink('/', '#pin=391722')).toBeNull();
    expect(parseJoinLink('/join/', '#pin=1')).toBeNull();
    expect(parseJoinLink('/join/Ops!', '')).toBeNull();
    expect(parseJoinLink('/joined/ops', '')).toBeNull();
    expect(parseJoinLink('/ops', '')).toBeNull();
  });

  it('ignores a fragment that does not carry a PIN', () => {
    expect(parseJoinLink('/join/ops', '#something')).toEqual({ room: 'ops', pin: '' });
  });
});
