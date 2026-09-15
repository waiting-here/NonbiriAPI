import assert from 'node:assert/strict';
import test from 'node:test';
import { replayRPSGesture, verifyRandomness } from '../../scripts/verify-game-randomness.mjs';

const vector = {
  algorithm: 'hmac-sha256-reject64-v1',
  game: 'blackjack',
  resource_id: 'bjt_AAAAAAAAAAAAAAAAAAAAAA',
  rules: 'six-decks-s17-v1',
  commitment: '5b14b84868c9d5d5c346ad5ee4bea248526db7e6fa1ac8916a40c6c3f28f4260',
  seed: '2a'.repeat(32),
  streams: [{ label: 'shoe', samples: 'AAAAAAAAATgAAAAAAAABBg==' }],
};

test('indexed RPS gestures match independent vectors at 128-bit phase boundaries', () => {
  const proof = { ...vector, game: 'rps', resource_id: 'rps_AAAAAAAAAAAAAAAAAAAAAA', rules: '2/standard', commitment: '6a1378f3663241ad969c6efdf34e1bc8bf4ad66029a8c8ce00826dadb6247e5b', streams: [] };
  for (let seat = 0; seat < 3; seat++) {
    assert.equal(replayRPSGesture(proof, seat, '1'), 'scissors');
    assert.equal(replayRPSGesture(proof, seat, '340282366920938463463374607431768211455'), 'paper');
  }
  for (const phase of ['0', '01', '-1', '340282366920938463463374607431768211456']) assert.throws(() => replayRPSGesture(proof, 0, phase));
  assert.throws(() => replayRPSGesture(proof, 3, '1'));
});

test('independent verifier agrees with the Go commitment and unsigned sample vector', () => {
  const result = verifyRandomness(vector, vector.commitment);
  assert.equal(result.valid, true);
  assert.equal(result.opening_commitment_checked, true);
  assert.deepEqual(result.samples, [{ stream: 'shoe', bound: '312', result: '262' }]);
});

test('offline verification rejects hidden seeds, changed openings and changed records', () => {
  assert.throws(() => verifyRandomness({ ...vector, seed: undefined }));
  assert.throws(() => verifyRandomness(vector, '00'.repeat(32)));
  for (const field of ['seed', 'game', 'resource_id', 'rules', 'commitment', 'algorithm']) {
    assert.throws(() => verifyRandomness({ ...vector, [field]: `${vector[field]}x` }));
  }
  const data = Buffer.from(vector.streams[0].samples, 'base64');
  data.writeBigUInt64BE(263n, 8);
  assert.throws(() => verifyRandomness({ ...vector, streams: [{ label: 'shoe', samples: data.toString('base64') }] }));
  assert.throws(() => verifyRandomness({ ...vector, streams: [...vector.streams, ...vector.streams] }));
  assert.throws(() => verifyRandomness({ ...vector, extra: true }));
});
