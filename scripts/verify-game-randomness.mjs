#!/usr/bin/env node
// Offline verification; no network calls or account credentials are required.
import { createHash, createHmac, timingSafeEqual } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { pathToFileURL } from 'node:url';

export const algorithm = 'hmac-sha256-reject64-v1';
const games = ['fishing', 'linklink', 'rps', 'bidding', 'likes', 'blackjack'];
const maxSamples = 65664;
const maxStreams = 1024;
const limit64 = 1n << 64n;
const text = (s, max) => typeof s === 'string' && s.length > 0 && s.length <= max && /^[!-~]+$/.test(s);
const fail = () => { throw new Error('Invalid or inconsistent random proof'); };

export function verifyRandomness(proof, openingCommitment) {
  if (!proof || typeof proof !== 'object' || Array.isArray(proof)) fail();
  const keys = ['algorithm', 'game', 'resource_id', 'rules', 'commitment', 'seed', 'streams'];
  if (Object.keys(proof).some(k => !keys.includes(k))) fail();
  if (proof.algorithm !== algorithm || !games.includes(proof.game) || !text(proof.resource_id, 64) || !text(proof.rules, 128) || !/^[a-f0-9]{64}$/.test(proof.seed) || !/^[a-f0-9]{64}$/.test(proof.commitment)) fail();
  if (openingCommitment !== undefined && openingCommitment !== proof.commitment) fail();
  const streams = proof.streams ?? [];
  if (!Array.isArray(streams) || streams.length > maxStreams) fail();
  const seed = Buffer.from(proof.seed, 'hex');
  const domain = kind => Buffer.from(`nonbiri/game-random/${algorithm}\0${kind}\0${proof.game}\0${proof.resource_id}\0${proof.rules}\0`, 'utf8');
  const commitment = createHash('sha256').update(domain('commit')).update(seed).digest();
  if (!timingSafeEqual(commitment, Buffer.from(proof.commitment, 'hex'))) fail();
  const seen = new Set();
  const samples = [];
  for (const stream of streams) {
    if (!stream || typeof stream !== 'object' || Object.keys(stream).some(k => !['label', 'samples'].includes(k)) || !text(stream.label, 96) || seen.has(stream.label) || typeof stream.samples !== 'string' || stream.samples.length > Math.ceil(maxSamples * 16 / 3) * 4) fail();
    seen.add(stream.label);
    const data = Buffer.from(stream.samples, 'base64');
    if (data.toString('base64') !== stream.samples || data.length % 16 !== 0 || samples.length + data.length / 16 > maxSamples) fail();
    let counter = 0n;
    for (let offset = 0; offset < data.length; offset += 16) {
      const bound = data.readBigUInt64BE(offset), expected = data.readBigUInt64BE(offset + 8);
      if (bound === 0n || expected >= bound) fail();
      const threshold = limit64 % bound;
      let actual;
      for (let tries = 0; tries < 128; tries++) {
        const bytes = Buffer.alloc(8);
        bytes.writeBigUInt64BE(counter++);
        const word = createHmac('sha256', seed).update(domain('draw')).update(stream.label + '\0').update(bytes).digest().readBigUInt64BE();
        if (word >= threshold) { actual = word % bound; break; }
      }
      if (actual !== expected) fail();
      samples.push({ stream: stream.label, bound: bound.toString(), result: actual.toString() });
    }
  }
  return { valid: true, algorithm, game: proof.game, resource_id: proof.resource_id, streams: streams.length, sample_count: samples.length, opening_commitment_checked: openingCommitment !== undefined, samples };
}

// Automatic RPS choices are indexed by public phase, so even very long games
// need no growing private transcript. Player choices are never derived here.
export function replayRPSGesture(proof, seat, phase) {
  verifyRandomness(proof);
  if (proof.game !== 'rps' || !Number.isInteger(seat) || seat < 0 || seat > 2 || typeof phase !== 'string' || !/^[1-9][0-9]{0,38}$/.test(phase) || BigInt(phase) >= (1n << 128n)) fail();
  const seed = Buffer.from(proof.seed, 'hex');
  const domain = Buffer.from(`nonbiri/game-random/${algorithm}\0indexed\0${proof.game}\0${proof.resource_id}\0${proof.rules}\0`, 'utf8');
  for (let counter = 0n; counter < 128n; counter++) {
    const bytes = Buffer.alloc(8); bytes.writeBigUInt64BE(counter);
    const word = createHmac('sha256', seed).update(domain).update(`automatic/${seat}/${phase}\0`).update(bytes).digest().readBigUInt64BE();
    if (word >= limit64 % 3n) return ['rock', 'scissors', 'paper'][Number(word % 3n)];
  }
  fail();
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    const args = process.argv.slice(2), flag = args.indexOf('--rps-action');
    const indexed = flag < 0 ? null : args.splice(flag);
    if (args.length < 1 || args.length > 2 || indexed && indexed.length !== 3) throw new Error('Usage: node scripts/verify-game-randomness.mjs proof.json [opening-commitment] [--rps-action seat phase]');
    const bytes = await readFile(args[0]);
    if (bytes.length > 2097184) throw new Error('Proof file is too large');
    const input = JSON.parse(bytes.toString('utf8'));
    const proof = Object.hasOwn(input, 'proof') ? input.proof : input;
    const result = verifyRandomness(proof, args[1]);
    if (indexed) result.rps_action = { seat: Number(indexed[1]), phase: indexed[2], gesture: replayRPSGesture(proof, Number(indexed[1]), indexed[2]) };
    console.log(JSON.stringify(result, null, 2));
  } catch (error) {
    console.error(error instanceof Error ? error.message : 'Verification failed');
    process.exitCode = 1;
  }
}
