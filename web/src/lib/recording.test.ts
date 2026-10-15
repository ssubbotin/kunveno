import { describe, expect, it, vi } from 'vitest';
import { deleteRecording, recordingLink, recordingUntil } from './recording';

const res = (status: number, body: unknown = {}, headers: Record<string, string> = {}) =>
  new Response(status === 204 ? null : JSON.stringify(body), { status, headers });

describe('recordingLink', () => {
  it('posts the PIN and returns the link', async () => {
    const f = vi.fn(async () => res(200, { url: '/api/rooms/ops/recording/ab12', expires: '2026-10-15T08:05:00Z' }));
    expect(await recordingLink(f, 'ops', '1234')).toEqual({ ok: true, url: '/api/rooms/ops/recording/ab12' });
    expect(f).toHaveBeenCalledWith('/api/rooms/ops/recording', expect.objectContaining({ method: 'POST', body: '{"pin":"1234"}' }));
  });
  it('maps failures', async () => {
    expect(await recordingLink(async () => res(403), 'ops', 'x')).toEqual({ ok: false, error: 'bad_pin' });
    expect(await recordingLink(async () => res(404), 'ops', 'x')).toEqual({ ok: false, error: 'gone' });
    expect(await recordingLink(async () => res(429, {}, { 'Retry-After': '30' }), 'ops', 'x')).toEqual({ ok: false, error: 'too_many', retryAfter: 30 });
    expect(await recordingLink(async () => { throw new Error('net'); }, 'ops', 'x')).toEqual({ ok: false, error: 'failed' });
  });
  it('rejects a link outside the room', async () => {
    expect(await recordingLink(async () => res(200, { url: 'https://evil.example/x' }), 'ops', '1')).toEqual({ ok: false, error: 'failed' });
  });
});

describe('deleteRecording', () => {
  it('maps 204 and 409', async () => {
    expect(await deleteRecording(async () => res(204), 'ops', '1')).toEqual({ ok: true });
    expect(await deleteRecording(async () => res(409), 'ops', '1')).toEqual({ ok: false, error: 'recording' });
  });
});

describe('recordingUntil', () => {
  it('reads a future time only', () => {
    expect(recordingUntil('2026-10-16T08:00:00Z', Date.parse('2026-10-15T08:00:00Z'))?.toISOString()).toBe('2026-10-16T08:00:00.000Z');
    expect(recordingUntil('2026-10-14T08:00:00Z', Date.parse('2026-10-15T08:00:00Z'))).toBeNull();
    expect(recordingUntil(undefined)).toBeNull();
  });
});
