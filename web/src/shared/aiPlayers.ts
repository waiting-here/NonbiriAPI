import { useTranslation } from 'react-i18next';

export function useAIText() {
  const { i18n } = useTranslation();
  return (zh: string, en: string) => (i18n.resolvedLanguage?.startsWith('zh') ? zh : en);
}
export function aiPlayerLabel(sourceID: string, t: ReturnType<typeof useAIText>) {
  return sourceID === 'bidding-local'
    ? t('第一代 AI 玩家', 'First-generation AI player')
    : t('AI 玩家', 'AI player');
}
export interface AITerms {
  bot_id: string;
  bot_name: string;
  description: string;
  revision: string;
  challenge_id: string;
  rules_key: string;
  policy_id: string;
  policy_version: number;
  source_id: string;
  policy_schema: string;
  first_reward: string;
  memory_days: number;
  memory_games: number;
}
export interface AIView {
  terms: AITerms;
  memory_enabled: boolean;
  memory_samples: number;
  first_clear: boolean;
  reward: string;
}
export interface AIActionSource {
  phase_seq?: string;
  round: number;
  phase: string;
  seat: number;
  action: unknown;
  origin: string;
  failure?: string;
  accepted_at?: number;
}
export interface AIHome {
  enabled: boolean;
  bots: {
    terms: { ticket: string; ai: AITerms };
    terms_hash: string;
    completed: boolean;
    memory_enabled: boolean;
    memory_samples: number;
  }[];
}
export interface AIParameters {
  hand_value: number;
  urgency: number;
  exploration: number;
  memory_weight: number;
  joker_cost: number;
  endgame_guard: boolean;
}
export interface AIRule {
  match: 'all' | 'any';
  conditions: { field: string; operator: string; value: number }[];
  override: Partial<AIParameters>;
  filter: string;
}
export interface AIPolicyDefinition {
  schema: string;
  parameters: AIParameters;
  rules: AIRule[];
}
export interface AIPolicy {
  id: string;
  name: string;
  description: string;
  enabled: boolean;
  revision: string;
  version: number;
  source_id: string;
  schema_id: string;
  definition: AIPolicyDefinition;
}
export interface AIBot {
  id: string;
  name: string;
  description: string;
  enabled: boolean;
  revision: string;
  policy_id: string;
  policy_version: number;
  challenge_id: string;
  ticket: string;
  first_reward: string;
  memory_days: number;
  memory_games: number;
}
export interface AIScenario {
  id: string;
  name: string;
  observation: {
    round: number;
    phase: string;
    seat: number;
    view: { hand_remaining: number[][]; pool_points: number; scores: number[] };
  };
}
export interface AIAdminState {
  settings: { enabled: boolean; revision: string };
  policies: AIPolicy[];
  bots: AIBot[];
  presets: { id: string; name: string; description: string; policy: AIPolicyDefinition }[];
  scenarios: AIScenario[];
}
export interface AIPreview {
  candidates: { id: string; score: number; probability: number; guaranteed_win: boolean }[];
  matched_rule: number;
  filter_empty: boolean;
  memory_weight: number;
}
