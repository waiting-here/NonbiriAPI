import type { AIAdminState, AITerms } from '../../src/shared/aiPlayers';
export const aiPolicy = {
  schema: 'bidding-local/v1',
  parameters: {
    hand_value: 0.65,
    urgency: 0.15,
    exploration: 0.03,
    memory_weight: 0.65,
    joker_cost: 0.25,
    endgame_guard: true,
  },
  rules: [],
};
export const aiTerms: AITerms = {
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
export function aiAdminFixture(): AIAdminState {
  return {
    settings: { enabled: false, revision: '1' },
    policies: [
      {
        id: aiTerms.policy_id,
        name: 'Patient policy',
        description: 'Keeps strong cards',
        enabled: true,
        revision: '1',
        version: 1,
        source_id: 'bidding-local',
        schema_id: aiPolicy.schema,
        definition: structuredClone(aiPolicy),
      },
    ],
    bots: [
      {
        id: aiTerms.bot_id,
        name: 'Patient',
        description: aiTerms.description,
        enabled: false,
        revision: '1',
        policy_id: aiTerms.policy_id,
        policy_version: 1,
        challenge_id: aiTerms.challenge_id,
        ticket: '0',
        first_reward: '0',
        memory_days: 30,
        memory_games: 30,
      },
    ],
    presets: [
      {
        id: 'balanced',
        name: 'Balanced',
        description: 'Default',
        policy: structuredClone(aiPolicy),
      },
    ],
    scenarios: [
      {
        id: 'opening',
        name: 'Opening bid',
        observation: {
          round: 1,
          phase: 'bid',
          seat: 0,
          view: {
            hand_remaining: [
              [1, 2, 3],
              [1, 2, 3],
            ],
            pool_points: 18,
            scores: [0, 0],
          },
        },
      },
    ],
  };
}
