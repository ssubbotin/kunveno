import { describe, expect, it } from 'vitest';
import { errorText } from './errors';

// Every code the server sends (internal/signal/protocol.go) has a translation of its own.
const serverCodes = ['bad_message', 'bad_room', 'bad_pin', 'room_full', 'not_joined', 'share_busy', 'media_failed', 'too_many_attempts', 'auth_required', 'bad_invite'];

describe('errorText', () => {
  it('translates every server error code', () => {
    for (const code of serverCodes) {
      const text = errorText('en', code, { retryAfter: 60 });
      expect(text, code).not.toContain(code);
      expect(text.length, code).toBeGreaterThan(5);
    }
  });
  it('never shows the server message for an unknown code', () => {
    expect(errorText('es', 'brand_new', {})).toBe('Error: brand_new');
  });
  it('fills the wait and the microphone detail', () => {
    expect(errorText('ru', 'too_many_attempts', { retryAfter: 42 })).toBe('Слишком много неверных PIN. Попробуйте через 42 с.');
    expect(errorText('en', 'no_mic', { detail: 'denied' })).toBe('Microphone access is required: denied');
  });
});
