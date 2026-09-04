import { describe, expect, it } from 'vitest';
import { parseRPSStreamEvent } from './stream';
import { rpsStateWire as stateWire } from './testFixtures';

const eventID = 'sse_AAAAAAAAAAAAAAAAAAAAAA';
describe('RPS shared account stream', () => {
  it('accepts complete matching snapshots and treats complete deltas as replacements', () => {
    for (const type of ['snapshot', 'delta'] as const) {
      const result = parseRPSStreamEvent(
        type,
        JSON.stringify({
          version: 1,
          channel: 'rps',
          type,
          revision: '2',
          identity_epoch: '3',
          occurred_at: 1_800_000_000,
          data: { kind: 'session', session: stateWire('gesture', '2', '3') },
        }),
        eventID,
      );
      expect(result.kind).toBe('replace');
    }
  });
  it('turns gaps, mismatched metadata, malformed payloads and oversized frames into resync', () => {
    expect(
      parseRPSStreamEvent(
        'gap',
        JSON.stringify({
          version: 1,
          channel: 'rps',
          type: 'gap',
          revision: null,
          identity_epoch: null,
          occurred_at: 1_800_000_000,
          data: { reason: 'slow_consumer', last_event_id: eventID },
        }),
        eventID,
      ),
    ).toEqual({ kind: 'resync', reason: 'gap' });
    expect(
      parseRPSStreamEvent(
        'delta',
        JSON.stringify({
          version: 1,
          channel: 'rps',
          type: 'delta',
          revision: '99',
          identity_epoch: '3',
          occurred_at: 1_800_000_000,
          data: { kind: 'session', session: stateWire('gesture', '2', '3') },
        }),
        eventID,
      ),
    ).toEqual({ kind: 'resync', reason: 'malformed' });
    expect(parseRPSStreamEvent('delta', '{', eventID)).toEqual({
      kind: 'resync',
      reason: 'malformed',
    });
    expect(parseRPSStreamEvent('delta', 'x'.repeat(16_385), eventID)).toEqual({
      kind: 'resync',
      reason: 'malformed',
    });
  });
  it('ignores the sibling activities channel without reading its data', () => {
    expect(
      parseRPSStreamEvent(
        'delta',
        JSON.stringify({
          version: 1,
          channel: 'activities',
          type: 'delta',
          revision: '1',
          identity_epoch: null,
          occurred_at: 1,
          data: { secret_like_unowned_field: 'ignored' },
        }),
        eventID,
      ),
    ).toEqual({ kind: 'ignore' });
  });
});
