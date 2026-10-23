import type { DiscoveryFormat } from '@shared/connections';
import type { NativeService } from '@shared/nativeServices';
import type { NumberedPage } from '../pageTypes';
import type { DiscoverySafeClass, DiscoveryState } from '../types';

export interface Connection {
  id: string;
  name: string;
  note: string;
  revision: string;
  entry_count: string;
  credential_count: string;
  created_at: number;
  updated_at: number;
}
export interface Credential {
  id: string;
  connection_id: string | null;
  note: string;
  enabled: boolean;
  revision: string;
  credential_version: string;
  display_head: string;
  display_tail: string;
  created_at: number;
  updated_at: number;
}
export interface CredentialVersion {
  credential_id: string;
  credential_version: string;
  enabled: boolean;
  revision: string;
  display_head: string;
  display_tail: string;
  created_at: number;
}
export type ImpactKind = 'personal' | 'charity' | 'discovery';
export interface ImpactEntry {
  endpoint_id: string | null;
  endpoint_key_id: string | null;
  donation_id: string | null;
  donation_key_id: string | null;
  use_id: string | null;
  discovery_source_id: string | null;
  credential_version: string;
  native_services: NativeService[];
  state: 'current' | 'approved' | 'pending';
}
export interface CredentialImpact extends NumberedPage<ImpactEntry> {
  kind: ImpactKind;
  summary: { endpoint_keys: string; donation_keys: string; discovery_sources: string };
}
export interface DiscoverySource {
  credential_available?: boolean;
  id: string;
  name: string;
  connection_id: string | null;
  base_url: string;
  list_format: DiscoveryFormat;
  models_url: string;
  credential_id: string;
  credential_version: string;
  revision: string;
  discovery_source_revision: string;
  enabled: boolean;
  state: DiscoveryState;
  checked_at: number | null;
  safe_class: DiscoverySafeClass;
  safe_diag: string;
  created_at: number;
  updated_at: number;
}
export interface DiscoveryModel {
  id: string;
  upstream_model_id: string;
  provider: string;
  native_services: NativeService[];
  service_evidence: 'legacy' | 'provider' | 'preset' | 'selected' | 'unknown';
  needs_service_selection: boolean;
}
export interface DiscoverySourceInput {
  name?: string;
  base_url: string;
  list_format: DiscoveryFormat;
  credential_id: string;
  enabled: boolean;
}
