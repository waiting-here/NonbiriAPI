import { useTranslation } from 'react-i18next';

export function useAIText() {
  const { i18n } = useTranslation();
  return (zh: string, en: string) => (i18n.resolvedLanguage?.startsWith('zh') ? zh : en);
}
export interface AITerms {
  bot_loadout?: unknown;
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
