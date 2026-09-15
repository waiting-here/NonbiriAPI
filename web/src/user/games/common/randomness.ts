import { exactRecord, invalidResponse } from './strict';

export const RANDOM_ALGORITHM = 'hmac-sha256-reject64-v1';
export const RANDOM_MAX_BYTES = 2 * 1024 * 1024;
const maxSamples = 65664;
const games = ['fishing', 'linklink', 'rps', 'bidding', 'likes', 'blackjack'] as const;
export type RandomGame = (typeof games)[number];
export interface RandomProof {
  readonly algorithm: typeof RANDOM_ALGORITHM;
  readonly game: RandomGame;
  readonly resource_id: string;
  readonly rules: string;
  readonly commitment: string;
  readonly seed?: string;
  readonly streams?: readonly { readonly label: string; readonly samples: string }[];
}
const ascii = (v: unknown, max: number): v is string =>
  typeof v === 'string' && v.length > 0 && v.length <= max && /^[!-~]+$/.test(v);
const hex = (v: unknown): v is string => typeof v === 'string' && /^[a-f0-9]{64}$/.test(v);

export function randomProof(value: unknown, game?: RandomGame, id?: string): RandomProof {
  const r = exactRecord(
    value,
    ['algorithm', 'game', 'resource_id', 'rules', 'commitment'],
    ['seed', 'streams'],
  );
  if (
    r.algorithm !== RANDOM_ALGORITHM ||
    !games.includes(r.game as RandomGame) ||
    !ascii(r.resource_id, 64) ||
    !ascii(r.rules, 128) ||
    !hex(r.commitment) ||
    (game !== undefined && r.game !== game) ||
    (id !== undefined && r.resource_id !== id)
  )
    invalidResponse('random commitment');
  if (r.seed !== undefined && !hex(r.seed)) invalidResponse('random seed');
  if (
    r.streams !== undefined &&
    (r.seed === undefined || !Array.isArray(r.streams) || r.streams.length > 1024)
  )
    invalidResponse('random transcript');
  let count = 0;
  const seen = new Set<string>();
  for (const item of (r.streams ?? []) as unknown[]) {
    const stream = exactRecord(item, ['label', 'samples']);
    if (
      !ascii(stream.label, 96) ||
      seen.has(stream.label) ||
      typeof stream.samples !== 'string' ||
      stream.samples.length > Math.ceil((maxSamples * 16) / 3) * 4
    )
      invalidResponse('random stream');
    seen.add(stream.label);
    let decoded: string;
    try {
      decoded = atob(stream.samples);
    } catch {
      return invalidResponse('random samples');
    }
    count += decoded.length / 16;
    if (decoded.length % 16 !== 0 || btoa(decoded) !== stream.samples || count > maxSamples)
      invalidResponse('random samples');
  }
  return r as unknown as RandomProof;
}

const bytes = (s: string) => new TextEncoder().encode(s);
const join = (...parts: Uint8Array[]) => {
  const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0));
  let offset = 0;
  for (const p of parts) {
    out.set(p, offset);
    offset += p.length;
  }
  return out;
};

// WebCrypto keeps verification asynchronous. No secret is requested until the
// server's projection discloses it; neither this verifier nor the UI can do so.
export async function verifyRandomProof(
  input: RandomProof,
  opening?: string,
  signal?: AbortSignal,
): Promise<number> {
  const p = randomProof(input);
  if (!p.seed || (opening !== undefined && p.commitment !== opening))
    throw new Error('Random proof mismatch');
  const seed = Uint8Array.from(p.seed.match(/../g)!, (s) => Number.parseInt(s, 16));
  const domain = (kind: string) =>
    bytes(
      `nonbiri/game-random/${RANDOM_ALGORITHM}\0${kind}\0${p.game}\0${p.resource_id}\0${p.rules}\0`,
    );
  const commitment = Array.from(
    new Uint8Array(await crypto.subtle.digest('SHA-256', join(domain('commit'), seed))),
    (v) => v.toString(16).padStart(2, '0'),
  ).join('');
  if (commitment !== p.commitment) throw new Error('Random proof mismatch');
  const key = await crypto.subtle.importKey('raw', seed, { name: 'HMAC', hash: 'SHA-256' }, false, [
    'sign',
  ]);
  let count = 0;
  for (const stream of p.streams ?? []) {
    const raw = Uint8Array.from(atob(stream.samples), (s) => s.charCodeAt(0));
    const data = new DataView(raw.buffer);
    const prefix = join(domain('draw'), bytes(stream.label + '\0'));
    let counter = 0n;
    for (let i = 0; i < raw.length; i += 16) {
      signal?.throwIfAborted();
      const bound = data.getBigUint64(i),
        expected = data.getBigUint64(i + 8);
      if (bound === 0n || expected >= bound) throw new Error('Random proof mismatch');
      const threshold = (1n << 64n) % bound;
      let result: bigint | undefined;
      for (let attempt = 0; attempt < 128; attempt++) {
        const sequence = new Uint8Array(8);
        new DataView(sequence.buffer).setBigUint64(0, counter++);
        const hash = await crypto.subtle.sign('HMAC', key, join(prefix, sequence));
        const word = new DataView(hash).getBigUint64(0);
        if (word >= threshold) {
          result = word % bound;
          break;
        }
      }
      if (result !== expected) throw new Error('Random proof mismatch');
      count++;
    }
  }
  return count;
}
