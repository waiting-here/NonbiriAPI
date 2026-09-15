import { webcrypto } from 'node:crypto';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { randomProof, verifyRandomProof } from './randomness';

const vector = {
  algorithm: 'hmac-sha256-reject64-v1',
  game: 'blackjack',
  resource_id: 'bjt_AAAAAAAAAAAAAAAAAAAAAA',
  rules: 'six-decks-s17-v1',
  commitment: '5b14b84868c9d5d5c346ad5ee4bea248526db7e6fa1ac8916a40c6c3f28f4260',
  seed: '2a'.repeat(32),
  streams: [{ label: 'shoe', samples: 'AAAAAAAAATgAAAAAAAABBg==' }],
};
afterEach(() => vi.unstubAllGlobals());
describe('public random proof boundary', () => {
  it('matches the independently pinned Go and Node cryptographic vector', async () => {
    vi.stubGlobal('crypto', webcrypto);
    await expect(verifyRandomProof(randomProof(vector), vector.commitment)).resolves.toBe(1);
    await expect(
      verifyRandomProof(randomProof({ ...vector, seed: '00'.repeat(32) })),
    ).rejects.toThrow();
    await expect(verifyRandomProof(randomProof(vector), '00'.repeat(32))).rejects.toThrow();
    const data = Uint8Array.from(atob(vector.streams[0].samples), (s) => s.charCodeAt(0));
    data[15] = 7;
    await expect(
      verifyRandomProof(
        randomProof({
          ...vector,
          streams: [{ label: 'shoe', samples: btoa(String.fromCharCode(...data)) }],
        }),
      ),
    ).rejects.toThrow();
  });
  it('rejects extra fields, wrong identities, malformed and oversized transcripts', () => {
    for (const p of [
      { ...vector, deck: [1] },
      { ...vector, seed: undefined },
      { ...vector, streams: [...vector.streams, ...vector.streams] },
      { ...vector, streams: [{ label: 'shoe', samples: '=' }] },
      { ...vector, streams: [{ label: 'shoe', samples: 'A'.repeat(2 << 20) }] },
    ])
      expect(() => randomProof(p)).toThrow();
    expect(() => randomProof(vector, 'likes')).toThrow();
    expect(() => randomProof(vector, 'blackjack', 'another')).toThrow();
    const opening = { ...vector, seed: undefined, streams: undefined };
    expect(randomProof(opening).seed).toBeUndefined();
  });
  it('cancels verification instead of continuing after unmount', async () => {
    vi.stubGlobal('crypto', webcrypto);
    const c = new AbortController();
    c.abort();
    await expect(verifyRandomProof(randomProof(vector), undefined, c.signal)).rejects.toThrow();
  });
});
