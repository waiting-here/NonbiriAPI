import { expect, it, vi } from 'vitest';
import { biddingCodec } from '../../bidding/normalize';
import { biddingHomeWire } from '../../bidding/testFixtures';
import type { AITerms } from '@shared/aiPlayers';
import { homeValue, resultValue } from './normalize';

const terms: AITerms = {
  bot_id: 'bot',
  bot_name: 'Bot',
  description: '',
  revision: '1',
  challenge_id: 'challenge',
  rules_key: 'gwent/v1',
  policy_id: 'policy',
  policy_version: 1,
  source_id: 'gwent-local',
  policy_schema: 'gwent-local/v1',
  first_reward: '0',
  memory_days: 0,
  memory_games: 0,
};
const deck = {
  faction: 'openai',
  leader: 'openai_leader',
  cards: [{ id: 'openai_gpt4', count: 1 }],
};
const decode = vi.fn((value: unknown) => ({ decoded: value }));
const codec = {
  ...biddingCodec,
  game: 'gwent' as const,
  modes: ['standard', 'ai'],
  loadout: decode,
};
const ai = {
  terms: { ...terms, bot_loadout: deck },
  memory_enabled: false,
  memory_samples: 0,
  first_clear: false,
  reward: '0',
};

it.each([
  ['gwent', 'gaq_'],
  ['bidding', 'aiq_'],
] as const)('reads the %s AI queue with its own prefix and frozen loadout', (game, queuePrefix) => {
  const wire = {
    server_now: 1000,
    current: null,
    latest_result: null,
    queue: {
      id: queuePrefix + 'AAAAAAAAAAAAAAAAAAAAAA',
      revision: '1',
      mode: 'ai',
      deadline: 2000,
      ticket: '0',
      payment: { general: '0', game: '0' },
      terms_hash: 'a'.repeat(64),
      rules_version: 1,
      ai: game === 'gwent' ? ai.terms : terms,
      ...(game === 'gwent' ? { loadout: deck } : {}),
    },
  };
  const actualCodec =
    game === 'gwent' ? codec : { ...biddingCodec, modes: [...biddingCodec.modes, 'ai'] };
  const result = homeValue(wire, actualCodec);
  expect(result.queue?.id).toBe(wire.queue.id);
  expect(result.queue?.ai?.bot_loadout).toEqual(game === 'gwent' ? { decoded: deck } : undefined);
  expect(() =>
    homeValue(
      {
        ...wire,
        queue: {
          ...wire.queue,
          id: (game === 'gwent' ? 'aiq_' : 'gaq_') + 'AAAAAAAAAAAAAAAAAAAAAA',
        },
      },
      actualCodec,
    ),
  ).toThrow();
});

it('decodes the frozen bot deck in active and completed Gwent AI views', () => {
  const base = biddingHomeWire();
  const current = {
    ...base.current,
    id: 'gwt_AAAAAAAAAAAAAAAAAAAAAA',
    game: 'gwent',
    mode: 'ai',
    phase: 'turn',
    ai,
  };
  expect(homeValue({ ...base, current }, codec).current?.ai?.terms.bot_loadout).toEqual({
    decoded: deck,
  });
  const completed = {
    id: current.id,
    game: 'gwent',
    mode: 'ai',
    terminal_at: 2000,
    outcome: 'win',
    reason: 'rounds',
    scores: [1, 0],
    own_payment: { general: '0', game: '0' },
    own_refund: { general: '0', game: '0' },
    prize_general: '0',
    rake: { platform: '0', welfare: '0', thursday: '0' },
    resolution: null,
    you: 0,
    view: null,
    profiles: current.profiles,
    ai,
  };
  expect(resultValue(completed, codec).ai?.terms.bot_loadout).toEqual({ decoded: deck });
  expect(homeValue(base, biddingCodec).current?.ai).toBeUndefined();
});
