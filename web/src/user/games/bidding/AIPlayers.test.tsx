import { screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { AIPlayers, AIMatchInfo } from './AIPlayers';
import { homeValue } from '../common/duel/normalize';
import { sendDuelIntent } from '../common/duel/api';
import { biddingCodec } from './normalize';
import { biddingHomeWire } from './testFixtures';
import type { AITerms } from '@shared/aiPlayers';
const terms: AITerms = {
  bot_id: 'bot_AAAAAAAAAAAAAAAAAAAAAA',
  bot_name: 'Patient',
  description: 'Keeps strong cards',
  revision: '1',
  challenge_id: 'aic_AAAAAAAAAAAAAAAAAAAAAA',
  rules_key: 'bidding/v1',
  policy_id: 'aip_AAAAAAAAAAAAAAAAAAAAAA',
  policy_version: 1,
  source_id: 'bidding-local',
  policy_schema: 'bidding-local/v1',
  first_reward: '0',
  memory_days: 30,
  memory_games: 30,
};
it('shows zero-price AI offers and submits an unpaid queue without a device token', async () => {
  const request = vi.fn();
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url, options) => {
      if (options?.method === 'POST') {
        request(JSON.parse(options.body));
        return new Response(
          JSON.stringify({ queue_id: 'aiq_AAAAAAAAAAAAAAAAAAAAAA', revision: '1', deadline: 2000 }),
          { status: 202, headers: { 'Content-Type': 'application/json' } },
        );
      }
      return new Response(
        JSON.stringify({
          enabled: true,
          bots: [
            {
              terms: { ticket: '0', ai: terms },
              terms_hash: 'a'.repeat(64),
              completed: false,
              memory_enabled: true,
              memory_samples: 0,
            },
          ],
        }),
        { headers: { 'Content-Type': 'application/json' } },
      );
    }),
  );
  const start = vi.fn();
  const rendered = await renderWithProviders(<AIPlayers blocked={false} onStart={start} />, {
    station: 'user',
  });
  await rendered.user.click(await screen.findByRole('button', { name: 'Challenge' }));
  expect(start).toHaveBeenCalledWith({
    kind: 'queue',
    mode: 'ai',
    botID: terms.bot_id,
    termsHash: 'a'.repeat(64),
  });
  await sendDuelIntent('bidding', start.mock.calls[0]![0], 'stable-key');
  expect(request).toHaveBeenCalledWith({
    mode: 'ai',
    bot_id: terms.bot_id,
    expected_terms_hash: 'a'.repeat(64),
  });
  expect(screen.getByText(/Turning this off keeps collecting samples/)).toBeVisible();
});
it('adapts free AI states and displays fallback status without exposing hidden actions', async () => {
  const old = biddingHomeWire();
  const ai = { terms, memory_enabled: false, memory_samples: 0, first_clear: false, reward: '0' };
  const home = homeValue(
    {
      ...old,
      current: {
        ...old.current,
        economy: 'ai_challenge',
        mode: 'ai',
        ticket: '0',
        own_payment: { general: '0', game: '0' },
        profiles: [{ kind: 'anonymous' }, { kind: 'ai', display_name: 'Patient' }],
        ai,
        action_sources: [
          {
            round: 1,
            phase: 'joker',
            seat: 1,
            action: { kind: 'joker', use: false },
            origin: 'fallback',
            failure: 'source_failure',
          },
        ],
      },
    },
    biddingCodec,
  );
  expect(home.current?.profiles[1]).toMatchObject({ kind: 'ai', displayName: 'Patient' });
  await renderWithProviders(
    <AIMatchInfo ai={home.current!.ai!} sources={home.current!.sources} />,
    { station: 'user' },
  );
  expect(screen.getByText(/first-clear eligibility remain valid/)).toBeVisible();
  expect(screen.getByText('Match memory off')).toBeVisible();
});
