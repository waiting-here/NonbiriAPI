import { array, integer, oneOf, record } from '@shared/operations/wire';

export const GATEWAY_ADAPTERS = [
  'openai_chat',
  'openai_responses',
  'anthropic_effort',
  'anthropic_adaptive',
  'anthropic_always_adaptive',
] as const;
export type GatewayAdapter = (typeof GATEWAY_ADAPTERS)[number];
export const GATEWAY_EFFORTS = [
  'none',
  'minimal',
  'low',
  'medium',
  'high',
  'xhigh',
  'max',
] as const;
export type GatewayEffort = (typeof GATEWAY_EFFORTS)[number];
export interface GatewayCapabilityPolicy {
  adapter: GatewayAdapter;
  efforts: GatewayEffort[];
  max_output_tokens: number;
  storage: 'reject' | 'openai' | 'omit_false';
  cache: 'reject' | 'anthropic';
}

export function gatewayAdapterEfforts(adapter: GatewayAdapter): readonly GatewayEffort[] {
  switch (adapter) {
    case 'openai_chat':
      return GATEWAY_EFFORTS.filter((value) => value !== 'max');
    case 'openai_responses':
      return GATEWAY_EFFORTS;
    case 'anthropic_always_adaptive':
      return ['low', 'medium', 'high', 'xhigh', 'max'];
    default:
      return ['low', 'medium', 'high', 'max'];
  }
}

export function normalizeGatewayCapabilityPolicy(
  value: unknown,
  label = 'Gateway capability',
): GatewayCapabilityPolicy {
  const policy = record(
    value,
    ['adapter', 'efforts', 'max_output_tokens', 'storage', 'cache'],
    label,
  );
  return {
    adapter: oneOf(policy.adapter, GATEWAY_ADAPTERS, `${label} adapter`),
    efforts: array(policy.efforts, `${label} efforts`, 7).map((value) =>
      oneOf(value, GATEWAY_EFFORTS, `${label} effort`),
    ),
    max_output_tokens: integer(policy.max_output_tokens, `${label} output ceiling`, 0, 2147483647),
    storage: oneOf(policy.storage, ['reject', 'openai', 'omit_false'], `${label} storage policy`),
    cache: oneOf(policy.cache, ['reject', 'anthropic'], `${label} cache policy`),
  };
}
