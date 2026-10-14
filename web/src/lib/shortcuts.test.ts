import { describe, expect, it } from 'vitest';
import { ariaShortcut, isMac, matchShortcut, shortcutLabel, type KeyLike } from './shortcuts';

const key = (code: string, mods: Partial<KeyLike> = {}): KeyLike => ({
  code, ctrlKey: false, metaKey: false, altKey: false, shiftKey: false, repeat: false, ...mods,
});

describe('matchShortcut', () => {
  it('maps Ctrl+D, Ctrl+E and Ctrl+/ off macOS', () => {
    expect(matchShortcut(key('KeyD', { ctrlKey: true }), false)).toBe('mic');
    expect(matchShortcut(key('KeyE', { ctrlKey: true }), false)).toBe('camera');
    expect(matchShortcut(key('Slash', { ctrlKey: true }), false)).toBe('help');
  });

  it('uses Meta on macOS and ignores Ctrl there', () => {
    expect(matchShortcut(key('KeyD', { metaKey: true }), true)).toBe('mic');
    expect(matchShortcut(key('KeyD', { ctrlKey: true }), true)).toBeNull();
    expect(matchShortcut(key('KeyD', { metaKey: true }), false)).toBeNull();
  });

  it('matches by physical key, so the Russian layout works', () => {
    // On the Russian layout D types "в"; code stays KeyD.
    expect(matchShortcut({ ...key('KeyD', { ctrlKey: true }), key: 'в' } as KeyLike, false)).toBe('mic');
  });

  it('rejects extra modifiers, AltGr included', () => {
    expect(matchShortcut(key('KeyD', { ctrlKey: true, shiftKey: true }), false)).toBeNull();
    expect(matchShortcut(key('KeyD', { ctrlKey: true, altKey: true }), false)).toBeNull();
    expect(matchShortcut(key('KeyD', { ctrlKey: true, metaKey: true }), false)).toBeNull();
  });

  it('rejects auto-repeat, plain keys and other letters', () => {
    expect(matchShortcut(key('KeyD', { ctrlKey: true, repeat: true }), false)).toBeNull();
    expect(matchShortcut(key('KeyD'), false)).toBeNull();
    expect(matchShortcut(key('Space'), false)).toBeNull();
    expect(matchShortcut(key('KeyM', { ctrlKey: true }), false)).toBeNull();
  });
});

describe('labels', () => {
  it('shows platform keys', () => {
    expect(shortcutLabel('mic', false)).toBe('Ctrl+D');
    expect(shortcutLabel('help', false)).toBe('Ctrl+/');
    expect(shortcutLabel('camera', true)).toBe('⌘E');
  });

  it('gives aria-keyshortcuts values', () => {
    expect(ariaShortcut('mic', false)).toBe('Control+D');
    expect(ariaShortcut('camera', true)).toBe('Meta+E');
    expect(ariaShortcut('help', false)).toBe('Control+/');
  });
});

describe('isMac', () => {
  it('reads userAgentData first, then platform', () => {
    expect(isMac({ userAgentData: { platform: 'macOS' } })).toBe(true);
    expect(isMac({ platform: 'MacIntel' })).toBe(true);
    expect(isMac({ platform: 'iPad' })).toBe(true);
    expect(isMac({ platform: 'Linux x86_64' })).toBe(false);
    expect(isMac({ userAgentData: { platform: 'Windows' }, platform: 'Win32' })).toBe(false);
    expect(isMac({})).toBe(false);
  });
});
