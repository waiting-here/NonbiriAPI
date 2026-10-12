import { QueryClient } from '@tanstack/react-query';
import { describe, expect, it } from 'vitest';
import { ApiError } from './query/http';
import {
  beginManagementSessionRequest,
  captureStationSession,
  clearManagementSession,
  failManagementSessionRequest,
  noteManagementSessionSuccess,
  stationSessionMatches,
  stationSessionWrite,
} from './charityManagement';

function login(client: QueryClient, id = '7', level = 3, username = 'sample') {
  const value = { user: { id, effective_level: level, username } };
  const serial = beginManagementSessionRequest(client, 'steward');
  expect(noteManagementSessionSuccess(client, 'steward', value, serial)).toBe(true);
  client.setQueryData(['user', 'session'], value);
}

describe('station session identity', () => {
  it('keeps in-flight business responses through same-account reads and profile edits', async () => {
    const client = new QueryClient();
    login(client);
    let finish!: (value: string) => void;
    const pending = stationSessionWrite(
      client,
      'steward',
      () =>
        new Promise<string>((resolve) => {
          finish = resolve;
        }),
    );
    const snapshot = captureStationSession(client, 'steward');
    beginManagementSessionRequest(client, 'steward');
    expect(stationSessionMatches(client, 'steward', snapshot)).toBe(true);
    login(client, '7', 3, 'renamed');
    finish('confirmed');
    await expect(pending).resolves.toBe('confirmed');
    client.clear();
  });

  it.each([
    ['8', 3],
    ['7', 4],
    ['7', 5],
    ['7', 6],
  ] as const)('invalidates account or exact permission changes: %s/%s', (id, level) => {
    const client = new QueryClient();
    login(client);
    const snapshot = captureStationSession(client, 'steward');
    client.setQueryData(['user', 'private'], 'old');
    login(client, id, level);
    expect(stationSessionMatches(client, 'steward', snapshot)).toBe(false);
    expect(client.getQueryData(['user', 'private'])).toBeUndefined();
    client.clear();
  });

  it('orders session reads and prevents a late completion after logout', () => {
    const client = new QueryClient();
    login(client);
    const old = beginManagementSessionRequest(client, 'steward');
    login(client, '8');
    expect(
      failManagementSessionRequest(client, 'steward', old, new ApiError('unauthorized', '', 401)),
    ).toBe(false);
    const pending = beginManagementSessionRequest(client, 'steward');
    clearManagementSession(client, 'steward');
    expect(
      noteManagementSessionSuccess(
        client,
        'steward',
        { user: { id: '8', username: 'sample', effective_level: 3 } },
        pending,
      ),
    ).toBe(false);
    client.clear();
  });

  it('retains the station on transport failures and domain rejection, but clears authoritative authentication failures', async () => {
    const client = new QueryClient();
    login(client);
    for (const error of [new TypeError('offline'), new ApiError('unavailable', '', 503)]) {
      const serial = beginManagementSessionRequest(client, 'steward');
      expect(failManagementSessionRequest(client, 'steward', serial, error)).toBe(false);
    }
    const denied = new ApiError('forbidden', '', 403);
    await expect(
      stationSessionWrite(client, 'steward', async () => {
        throw denied;
      }),
    ).rejects.toBe(denied);
    expect(client.getQueryData(['user', 'session'])).not.toBeNull();
    const serial = beginManagementSessionRequest(client, 'steward');
    expect(failManagementSessionRequest(client, 'steward', serial, denied)).toBe(true);
    expect(client.getQueryData(['user', 'session'])).toBeNull();
    client.clear();
  });
});
