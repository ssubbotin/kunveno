import { afterEach, describe, expect, it, vi } from 'vitest';
import { InviteFailure, createInvite, failureFor, listInvites, parseInvites, revokeInvite } from './invites';

type Call = { url: string; method: string; body: unknown };

function serve(status: number, body: unknown, headers: Record<string, string> = {}): Call[] {
  const calls: Call[] = [];
  vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit) => {
    calls.push({ url, method: init.method ?? 'GET', body: JSON.parse(String(init.body)) });
    return new Response(status === 204 ? null : JSON.stringify(body), { status, headers });
  }));
  return calls;
}

describe('invitations', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('creates a link with the PIN and the chosen hours', async () => {
    const calls = serve(201, { id: 'i1', link: 'https://meet.example.com/join/ops#invite=tok', expires: '2026-10-17T10:00:00Z' });
    expect(await createInvite('ops', '1234', 24)).toEqual({ id: 'i1', link: 'https://meet.example.com/join/ops#invite=tok', expires: new Date('2026-10-17T10:00:00Z') });
    expect(calls).toEqual([{ url: '/api/rooms/ops/invites', method: 'POST', body: { pin: '1234', hours: 24 } }]);
  });

  it('lists and revokes with the PIN', async () => {
    let calls = serve(200, [{ id: 'i1', created_by: 'Анна', expires: '2026-10-17T10:00:00Z' }]);
    expect(await listInvites('ops', '1234')).toEqual([{ id: 'i1', createdBy: 'Анна', expires: new Date('2026-10-17T10:00:00Z') }]);
    expect(calls).toEqual([{ url: '/api/rooms/ops/invites/list', method: 'POST', body: { pin: '1234' } }]);
    calls = serve(204, null);
    await revokeInvite('ops', '1234', 'i1');
    expect(calls).toEqual([{ url: '/api/rooms/ops/invites/i1', method: 'DELETE', body: { pin: '1234' } }]);
  });

  it('drops malformed list entries', () => {
    expect(parseInvites([{ id: 'a', created_by: 'A', expires: '2026-10-17T10:00:00Z' }, { id: 1 }, { id: 'b', created_by: 'B', expires: 'soon' }, null])).toEqual([
      { id: 'a', createdBy: 'A', expires: new Date('2026-10-17T10:00:00Z') },
    ]);
    expect(parseInvites({})).toEqual([]);
  });

  it('turns refusals into texts of their own', async () => {
    expect(failureFor(401, null)).toMatchObject({ key: 'auth.expired' });
    expect(failureFor(403, null)).toMatchObject({ key: 'error.bad_pin' });
    expect(failureFor(400, null)).toMatchObject({ key: 'invite.tooLong' });
    expect(failureFor(429, '42')).toMatchObject({ key: 'pin.tooMany', vars: { seconds: 42 } });
    expect(failureFor(429, null)).toMatchObject({ key: 'pin.tooMany', vars: { seconds: 60 } });
    expect(failureFor(500, null)).toMatchObject({ key: 'invite.failed' });
    serve(400, { error: 'hours must be 1, 24 or 168' });
    await expect(createInvite('ops', '1234', 168)).rejects.toBeInstanceOf(InviteFailure);
    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('offline'); }));
    await expect(listInvites('ops', '1234')).rejects.toMatchObject({ key: 'invite.failed' });
  });
});
