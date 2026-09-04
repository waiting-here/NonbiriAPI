import { describe, expect, it, vi } from 'vitest';
import { createOperationIdentity } from './request';

describe('operation identity generation', () => {
  it('preserves every random byte in the secure fallback encoding', () => {
    let call = 0;
    const getRandomValues = vi.fn((bytes: Uint8Array) => {
      bytes.fill(call === 0 ? 0x00 : 0x24);
      call += 1;
      return bytes;
    });
    vi.stubGlobal('crypto', { getRandomValues });

    expect(createOperationIdentity()).toEqual({
      idempotencyKey: '00'.repeat(24),
      actionId: '24'.repeat(24),
    });
    expect(getRandomValues).toHaveBeenCalledTimes(2);
  });
});
