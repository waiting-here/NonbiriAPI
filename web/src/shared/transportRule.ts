import { oneOf } from '@shared/operations/wire';

export const transportRules = ['passthrough', 'force_non_stream', 'force_stream'] as const;
export type TransportRule = (typeof transportRules)[number];
export function normalizeTransportRule(value: unknown): TransportRule {
  return oneOf(value, transportRules, 'model transport rule');
}
