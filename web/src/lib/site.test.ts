import { describe, expect, it } from 'vitest';
import { DEFAULT_REACTIONS, infoFrom } from './site.svelte';

describe('infoFrom', () => {
  it('takes the configured title and reactions', () => {
    expect(infoFrom({ title: 'Конференц-связь kunveno. Частный сервер.', reactions: ['👍', '☕'] }))
      .toEqual({ title: 'Конференц-связь kunveno. Частный сервер.', reactions: ['👍', '☕'], noiseSuppression: false, notesEnabled: false, notesLive: false, recordingEnabled: false });
  });
  it('falls back to the defaults', () => {
    expect(infoFrom({})).toEqual({ title: 'kunveno', reactions: DEFAULT_REACTIONS, noiseSuppression: false, notesEnabled: false, notesLive: false, recordingEnabled: false });
    expect(infoFrom({ title: '   ', reactions: [] })).toEqual({ title: 'kunveno', reactions: DEFAULT_REACTIONS, noiseSuppression: false, notesEnabled: false, notesLive: false, recordingEnabled: false });
    expect(infoFrom(null)).toEqual({ title: 'kunveno', reactions: DEFAULT_REACTIONS, noiseSuppression: false, notesEnabled: false, notesLive: false, recordingEnabled: false });
  });
  it('ignores malformed reactions', () => {
    expect(infoFrom({ reactions: ['👍', 42, '', 'x'.repeat(40)] }).reactions).toEqual(['👍']);
  });
  it('defaults include thumbs down and coffee', () => {
    expect(DEFAULT_REACTIONS).toContain('👎');
    expect(DEFAULT_REACTIONS).toContain('☕');
  });
});

describe('infoFrom noise_suppression', () => {
  it('reads noise_suppression as a boolean only', () => {
    expect(infoFrom({ noise_suppression: true }).noiseSuppression).toBe(true);
    expect(infoFrom({ noise_suppression: 'true' }).noiseSuppression).toBe(false);
    expect(infoFrom({}).noiseSuppression).toBe(false);
  });
});

describe('infoFrom notes', () => {
  it('reads notes and notes_live as booleans only', () => {
    expect(infoFrom({ notes: true, notes_live: true })).toMatchObject({ notesEnabled: true, notesLive: true });
    expect(infoFrom({ notes: 'true', notes_live: 1 })).toMatchObject({ notesEnabled: false, notesLive: false, recordingEnabled: false });
    expect(infoFrom({ notes_live: true })).toMatchObject({ notesEnabled: false, notesLive: false, recordingEnabled: false });
  });
  it('reads the recording flag', () => {
    expect(infoFrom({ recording: true }).recordingEnabled).toBe(true);
    expect(infoFrom({}).recordingEnabled).toBe(false);
  });
});
