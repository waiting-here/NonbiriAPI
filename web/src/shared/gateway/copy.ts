import type { GatewayAdapter, GatewayCapabilityPolicy, GatewayEffort } from './capabilities';

export const gatewayAdapterLabels: Record<GatewayAdapter, string> = {
  openai_chat: 'gatewayCapabilities.adapters.openai_chat',
  openai_responses: 'gatewayCapabilities.adapters.openai_responses',
  anthropic_effort: 'gatewayCapabilities.adapters.anthropic_effort',
  anthropic_adaptive: 'gatewayCapabilities.adapters.anthropic_adaptive',
  anthropic_always_adaptive: 'gatewayCapabilities.adapters.anthropic_always_adaptive',
};
export const gatewayEffortLabels: Record<GatewayEffort, string> = {
  none: 'gatewayCapabilities.effort.none',
  minimal: 'gatewayCapabilities.effort.minimal',
  low: 'gatewayCapabilities.effort.low',
  medium: 'gatewayCapabilities.effort.medium',
  high: 'gatewayCapabilities.effort.high',
  xhigh: 'gatewayCapabilities.effort.xhigh',
  max: 'gatewayCapabilities.effort.max',
};
export const gatewayStorageLabels: Record<GatewayCapabilityPolicy['storage'], string> = {
  reject: 'gatewayCapabilities.storageOptions.reject',
  openai: 'gatewayCapabilities.storageOptions.openai',
  omit_false: 'gatewayCapabilities.storageOptions.omit_false',
};
export const gatewayCacheLabels: Record<GatewayCapabilityPolicy['cache'], string> = {
  reject: 'gatewayCapabilities.cacheOptions.reject',
  anthropic: 'gatewayCapabilities.cacheOptions.anthropic',
  anthropic_explicit: 'gatewayCapabilities.cacheOptions.anthropic_explicit',
};
