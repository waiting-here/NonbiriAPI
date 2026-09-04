import { afterEach, describe, expect, it, vi } from 'vitest';
import { gameRequest } from './request';

describe('bounded game transport', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('rejects a streamed response as soon as it crosses the game response limit', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response('x'.repeat(256 * 1024 + 1), { status: 200 })),
    );
    await expect(gameRequest('/api/games/test')).rejects.toMatchObject({
      code: 'invalid_response',
      status: 200,
    });
  });

  it('rejects malformed UTF-8 before JSON parsing', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response(Uint8Array.of(0xc3, 0x28), { status: 200 })),
    );
    await expect(gameRequest('/api/games/test')).rejects.toMatchObject({
      code: 'invalid_response',
      status: 200,
    });
  });
});
