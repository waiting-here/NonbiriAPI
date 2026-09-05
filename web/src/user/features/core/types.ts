export const CONNECTOR_TYPES = ['openai-compatible', 'anthropic-compatible'] as const;

export type ConnectorType = (typeof CONNECTOR_TYPES)[number];
export type AccountLanguage = '' | 'zh' | 'en';
export type ExplicitLanguage = Exclude<AccountLanguage, ''>;
export type RouteStrategy = 'ordered' | 'random';
export type CatalogSourceType = 'automatic' | 'manual';
export type SuspensionState = 'none' | 'security_processing';

export interface Page<T> {
  data: T[];
  next_cursor: string | null;
}

export interface UsageSummary {
  total_requests: string;
  total_uncached_input_tokens: string;
  total_cache_write_input_tokens: string;
  total_cache_read_input_tokens: string;
  total_output_tokens: string;
  total_prompt_tokens: string;
  total_completion_tokens: string;
  total_unknown_usage_requests: string;
}

export interface UserProfile {
  id: string;
  username: string;
  avatar: string | null;
  avatar_url: string | null;
  guild_nick: string | null;
  guild_avatar_url: string | null;
  lang: AccountLanguage;
  is_banned: boolean;
  banned_until: number | null;
  charity_suspended_until: number | null;
  endpoint_limit: string | null;
  effective_endpoint_limit: string;
  rpm_limit: string | null;
  effective_rpm_limit: string;
  concurrency_limit: string | null;
  effective_concurrency_limit: string;
  balance: string;
  donation_credit: string;
  effective_level: 1 | 2 | 3 | 4 | 5;
  level_display_name: string;
  game_profile_public: boolean;
  created_at: number;
  updated_at: number;
  usage: UsageSummary;
}

export interface UserEnvelope {
  user: UserProfile;
}

export type EndpointSource = 'mainstream' | 'custom';

export type EndpointOrigin =
  { kind: 'custom' } | { kind: 'mainstream'; channel_id: string; name: string };

export interface MainstreamChannelOption {
  id: string;
  name: string;
  connector_type: ConnectorType;
  base_url: string;
}

export interface EndpointCreateOptions {
  base_connector_types: ConnectorType[];
  mainstream_channels: MainstreamChannelOption[];
}

export interface Endpoint {
  id: string;
  connector_type: ConnectorType;
  base_url: string;
  origin: EndpointOrigin;
  note: string;
  enabled: boolean;
  revision: string;
  key_count: string;
  created_at: number;
  updated_at: number;
}

export interface EndpointKey {
  id: string;
  endpoint_id: string;
  display_head: string;
  display_tail: string;
  note: string;
  enabled: boolean;
  force_store_false: boolean;
  max_concurrency: number;
  max_rpm: number;
  suspension_state: SuspensionState;
  revision: string;
  created_at: number;
  updated_at: number;
}

export interface CallerKeyMetadata {
  display: string;
  created_at: number;
  updated_at: number;
  generation: string;
}

export interface CallerKeyAuthority {
  generation: string;
  metadata: CallerKeyMetadata | null;
}

export interface CallerKeySecret {
  secret: string;
  metadata: CallerKeyMetadata;
}

export type DiscoveryState = 'unknown' | 'checking' | 'succeeded' | 'failed';
export type DiscoveryResult = 'empty' | 'nonempty' | null;
export type DiscoverySafeClass =
  'none' | 'auth' | 'rate_limit' | 'timeout' | 'protocol' | 'transport' | 'interrupted';

export interface DiscoveryEvidence {
  state: DiscoveryState;
  revision: string;
  result: DiscoveryResult;
  safe_class: DiscoverySafeClass;
  observed_at: number | null;
  count: string | null;
}

export interface CatalogEntry {
  id: string;
  source_type: CatalogSourceType;
  upstream_model_id: string;
  provider: string;
  source_revision: string;
  pair_revision: string;
  created_at: number;
  updated_at: number;
}

export interface CatalogView {
  evidence: DiscoveryEvidence;
  automatic_entries: CatalogEntry[];
  manual_entries: CatalogEntry[];
  next_cursor: string | null;
}

export interface Model {
  id: string;
  provider: string;
  model: string;
  full_name: string;
  route_strategy: RouteStrategy;
  silent_retry: boolean;
  flatten_tool_calls: boolean;
  revision: string;
  binding_revision: string;
  binding_count: string;
  created_at: number;
  updated_at: number;
}

export interface BindingCandidate {
  endpoint_key_id: string;
  endpoint_base_url: string;
  connector_type: ConnectorType;
  endpoint_note: string;
  endpoint_key_display_head: string;
  endpoint_key_display_tail: string;
  endpoint_key_note: string;
  upstream_model_id: string;
  source_types: CatalogSourceType[];
}

export interface Binding {
  id: string;
  endpoint_key_id: string;
  endpoint_base_url: string;
  connector_type: ConnectorType;
  endpoint_note: string;
  endpoint_key_display_head: string;
  endpoint_key_display_tail: string;
  endpoint_key_note: string;
  upstream_model_id: string;
  ord: number;
}

export interface BindingsResponse {
  bindings: Binding[];
  binding_revision: string;
}

export interface DiscoveryAccepted {
  operation_id: string;
  evidence: DiscoveryEvidence;
}

export interface ManualEntriesResponse {
  entries: CatalogEntry[];
}

export interface AffectedModel {
  model: Model;
  bindings: Binding[];
}

export interface ManualUpdateResponse {
  entries: CatalogEntry[];
  affected_models: AffectedModel[];
}

export interface OperationIdentity {
  idempotencyKey: string;
  actionId: string;
}

export type EndpointCreateInput =
  | {
      source: 'mainstream';
      channel_id: string;
      note: string;
      enabled: boolean;
    }
  | {
      source: 'custom';
      connector_type: ConnectorType;
      base_url: string;
      note: string;
      enabled: boolean;
    };

export interface EndpointPatchInput {
  note?: string;
  enabled?: boolean;
  expected_revision: string;
}

export interface EndpointKeyCreateInput {
  secret: string;
  note: string;
  enabled: boolean;
  force_store_false: boolean;
  ownership_confirmed: true;
  max_concurrency?: number;
  max_rpm?: number;
}

export interface EndpointKeyPatchInput {
  note?: string;
  enabled?: boolean;
  force_store_false?: boolean;
  expected_revision: string;
  max_concurrency?: number;
  max_rpm?: number;
}

export interface ModelCreateInput {
  provider: string;
  model: string;
  route_strategy?: RouteStrategy;
  silent_retry?: boolean;
  flatten_tool_calls?: boolean;
}

export interface ModelPatchInput {
  provider?: string;
  model?: string;
  route_strategy?: RouteStrategy;
  silent_retry?: boolean;
  flatten_tool_calls?: boolean;
  expected_revision: string;
}

export interface BindingSelection {
  endpoint_key_id: string;
  upstream_model_id: string;
}

export interface BindingReplacement {
  binding_id: string;
  replacement_upstream_model_id: string;
}

export interface CandidateFilters {
  endpointId?: string;
  keyId?: string;
  source?: CatalogSourceType;
  query?: string;
  cursor?: string;
  limit?: number;
}

export type HomeCapability<T> =
  { state: 'available'; load: (signal?: AbortSignal) => Promise<T> } | { state: 'unavailable' };

export type HomeCheckinStatus =
  | { enabled: false }
  | {
      enabled: true;
      checked_in_today: boolean;
      balance: string;
      award_min: string;
      award_max: string;
      balance_cap: string;
    };

export interface HomeCheckinResult {
  award: string;
  balance: string;
}

export type HomeCheckinCapability =
  | {
      state: 'available';
      load: (signal?: AbortSignal) => Promise<HomeCheckinStatus>;
      submit: (signal?: AbortSignal) => Promise<HomeCheckinResult>;
    }
  | { state: 'unavailable' };

export type HomeGameSummary =
  | {
      game: 'fishing';
      route_id: 'game-fishing';
      kind: 'continue';
      resource_id: string;
      state: 'settlement_pending' | 'recovery_required';
    }
  | {
      game: 'linklink';
      route_id: 'game-linklink';
      kind: 'continue';
      resource_id: string;
      state: 'active';
    }
  | {
      game: 'rps';
      route_id: 'game-rps';
      kind: 'continue';
      resource_id: string;
      state: 'started' | 'terminal_processing';
    }
  | {
      game: 'fishing';
      route_id: 'game-fishing';
      kind: 'view';
      resource_id: string;
      created_at: number;
    }
  | {
      game: 'rps';
      route_id: 'game-rps';
      kind: 'view';
      resource_id: string;
      created_at: number;
    };

export interface HomeAnnouncementSummary {
  id: string;
  title: string;
  excerpt: string;
}

export interface HomeAdapters {
  checkin: HomeCheckinCapability;
  games: HomeCapability<HomeGameSummary[]>;
  announcements: HomeCapability<HomeAnnouncementSummary[]>;
}

export type LifecycleIntent = 'export' | 'delete';

export interface AccountExportAttachment {
  blob: Blob;
  schemaVersion: 4;
}

export type AccountAuthority = 'active' | 'deleted';

export interface AccountLifecycleAdapter {
  capabilities: Readonly<{ exportV4: boolean; deleteAccount: boolean }>;
  beginElevation(intent: LifecycleIntent, accountId: string): Promise<string>;
  exportV4(input: { accountId: string; elevatedToken: string }): Promise<AccountExportAttachment>;
  deleteAccount(input: {
    accountId: string;
    elevatedToken: string;
    confirmation: 'DELETE';
  }): Promise<void>;
  readAccountAuthority(accountId: string): Promise<AccountAuthority>;
}
