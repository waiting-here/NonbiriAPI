import { describe, expect, it } from 'vitest';
import { rpsDeviceToken } from './device';

describe('RPS browser device identity', () => {
  it('uses one non-account storage key and reuses a raw-base64url 32-byte token', () => {
    const values = new Map<string, string>();
    const storage = {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => {
        values.set(key, value);
      },
    };
    const first = rpsDeviceToken(storage);
    const second = rpsDeviceToken(storage);
    expect(first).toBe(second);
    expect(first).toMatch(/^[A-Za-z0-9_-]{43}$/);
    expect([...values.keys()]).toEqual(['nonbiri.game.rps.device.v1']);
    expect([...values.keys()][0]).not.toMatch(/account|user|discord/i);
  });
});
