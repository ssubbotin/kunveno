import { describe, expect, it } from 'vitest';
import { NONE, parseChoice, resolveChoice, sameEffect } from './choice';

describe('background choice', () => {
  it('accepts the four kinds and nothing else', () => {
    expect(parseChoice({ kind: 'blur' })).toEqual({ kind: 'blur' });
    expect(parseChoice({ kind: 'image', source: 'server', name: 'a.jpg' })).toEqual({ kind: 'image', source: 'server', name: 'a.jpg' });
    expect(parseChoice({ kind: 'image', source: 'own' })).toEqual({ kind: 'image', source: 'own' });
    expect(parseChoice({ kind: 'image', source: 'server', name: '' })).toEqual(NONE);
    expect(parseChoice({ kind: 'image', source: 'server', name: '../x' })).toEqual(NONE);
    expect(parseChoice('blur')).toEqual(NONE);
    expect(parseChoice(undefined)).toEqual(NONE);
  });
  it('falls back to none when the chosen image is gone', () => {
    expect(resolveChoice({ kind: 'image', source: 'server', name: 'a.jpg' }, ['b.jpg'], false)).toEqual({ choice: NONE, lost: true });
    expect(resolveChoice({ kind: 'image', source: 'server', name: 'a.jpg' }, ['a.jpg'], false)).toEqual({ choice: { kind: 'image', source: 'server', name: 'a.jpg' }, lost: false });
    expect(resolveChoice({ kind: 'image', source: 'own' }, [], false)).toEqual({ choice: NONE, lost: true });
    expect(resolveChoice({ kind: 'blur' }, [], false)).toEqual({ choice: { kind: 'blur' }, lost: false });
  });
  it('tells switches inside the effect from ones that need the camera reopened', () => {
    expect(sameEffect({ kind: 'blur' }, { kind: 'image', source: 'own' })).toBe(true);
    expect(sameEffect(NONE, { kind: 'blur' })).toBe(false);
    expect(sameEffect({ kind: 'blur' }, NONE)).toBe(false);
  });
});
