import type { ConnectorType, ServiceProjection } from '../../src/shared/connections';
import type { BindingTarget, Endpoint, EndpointKey } from '../../src/user/features/core/types';

export const endpointScope = {
  connection_id: null,
  connection_name: '',
  configuration_type: 'openai-compatible',
  native_services: ['chat_completions'],
  rerank_format: '',
  rerank_path: '',
  rerank_url: '',
  rerank_path_configurable: false,
} satisfies Partial<Endpoint>;

export const credentialGrant = {
  credential_available: true,
  credential_id: '7',
  credential_version: '1',
  grant_revision: '1',
  discovery_source_id: null,
  native_services: ['chat_completions'],
} satisfies Partial<EndpointKey>;

export const channelPreset = {
  revision: '1',
  entries: [],
  discovery_source: null,
  model_mappings: [],
};
export const chatProjection: ServiceProjection = {
  native_services: ['chat_completions'],
  model_types: ['chat_completions'],
  service_routes: [
    { operation: 'chat_completions', native_service: 'chat_completions', mode: 'native' },
  ],
  capabilities: { can_bind: true, unavailable_reason: null },
};
export const unboundCandidate = {
  current_binding_id: null,
  other_binding_count: '0',
  other_bindings: [],
};

export function adaptationTarget(id: string, connector: ConnectorType): BindingTarget {
  const gateway = connector === 'ai-sdk-gateway-v3' || connector === 'ai-sdk-gateway-v4';
  const native_service = gateway ? 'gateway_text' : 'chat_completions';
  return {
    id,
    revision: '1',
    endpoint_id: id,
    endpoint_key_id: id,
    base_url: 'https://example.test',
    connector_type: connector,
    configuration_type: connector,
    credential_id: id,
    credential_version: '1',
    grant_revision: '1',
    upstream_model_id: 'test-model',
    native_services: [native_service],
    model_types: ['chat_completions'],
    service_routes: [
      { operation: 'chat_completions', native_service, mode: gateway ? 'converted' : 'native' },
    ],
    capabilities: { can_bind: true, unavailable_reason: null },
  };
}
