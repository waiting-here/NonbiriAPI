import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../../test/unit/support';
import { biddingCodec } from '../../bidding/normalize';
import { biddingHomeWire, biddingID } from '../../bidding/testFixtures';
import { sendDuelIntent, useDuel } from './api';
import { DuelFeedback } from './Feedback';

const json = (data: unknown, status = 200) =>
  new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } });
describe('two-player request recovery', () => {
  it('keeps the same body and idempotency key after an uncertain response, despite duplicate clicks', async () => {
    const requests: { key: string | null; body: string }[] = [];
    const fetch = vi.fn(async (_url: unknown, options?: RequestInit) => {
      if (options?.method === 'POST') {
        requests.push({
          key: new Headers(options.headers).get('Idempotency-Key'),
          body: String(options.body),
        });
        if (requests.length === 1) throw new TypeError('lost response');
        return json({ session_id: biddingID, revision: '2', phase_seq: '1', locked: true });
      }
      return json(biddingHomeWire());
    });
    vi.stubGlobal('fetch', fetch);
    function Harness() {
      const duel = useDuel(biddingCodec);
      return (
        <>
          <button
            disabled={duel.blocked}
            onClick={() => {
              duel.run({
                kind: 'action',
                id: biddingID,
                phaseSeq: '1',
                action: { kind: 'bid', card: 7 },
              });
              duel.run({
                kind: 'action',
                id: biddingID,
                phaseSeq: '1',
                action: { kind: 'bid', card: 13 },
              });
            }}
          >
            Lock
          </button>
          <DuelFeedback
            error={duel.error}
            pending={duel.pending}
            uncertain={duel.uncertain}
            onRetry={duel.retry}
          />
        </>
      );
    }
    const rendered = await renderWithProviders(<Harness />, { station: 'user' });
    await waitFor(() => expect(screen.getByRole('button', { name: 'Lock' })).toBeEnabled());
    await rendered.user.click(screen.getByRole('button', { name: 'Lock' }));
    await rendered.user.click(await screen.findByRole('button', { name: 'Retry same request' }));
    await waitFor(() => expect(requests).toHaveLength(2));
    expect(requests[0]).toEqual(requests[1]);
    expect(JSON.parse(requests[0].body)).toEqual({
      phase_seq: '1',
      action: { kind: 'bid', card: 7 },
    });
  });
  it('rejects a receipt for a different session', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        json({
          session_id: 'bid_AAAAAAAAAAAAAAAAAAAABA',
          revision: '2',
          phase_seq: '1',
          locked: true,
        }),
      ),
    );
    await expect(
      sendDuelIntent(
        'bidding',
        { kind: 'action', id: biddingID, phaseSeq: '1', action: { kind: 'bid', card: 7 } },
        'safe-key',
      ),
    ).rejects.toThrow();
  });
  it('never treats a queue or terminal financial response as an action receipt', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        json({ queue_id: 'bidq_AAAAAAAAAAAAAAAAAAAAAA', revision: '1', deadline: 2000 }, 202),
      ),
    );
    await expect(
      sendDuelIntent('bidding', { kind: 'surrender', id: biddingID, phaseSeq: '1' }, 'safe-key'),
    ).rejects.toThrow();
  });
});
