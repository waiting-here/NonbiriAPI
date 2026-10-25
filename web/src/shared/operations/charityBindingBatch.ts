import { decoded, idempotentOptions, queryPath } from './api';
import type { CharityRole } from './charity';
import { normalizeNumberedPage, validateWindow } from './numberedPage';
import type { PageSize } from './pageNumbers';
import {
  array,
  boolean,
  decimal,
  decimalID,
  invalidResponse,
  nullableDecimal,
  nullableString,
  oneOf,
  opaqueID,
  record,
  string,
} from './wire';

export interface BindingOption {
  charity_model_id: string;
  full_name: string;
  enabled: boolean;
  binding_revision: string;
}
export interface PresetChannel {
  channel_id: string;
  name: string;
  connector_type: string;
  base_url: string;
  enabled: boolean;
  state: 'active' | 'retired';
  channel_revision: string;
  preset_revision: string;
  mapping_count: string;
  recommended?: boolean;
}
export interface PresetMapping {
  upstream_model_id: string;
  charity_model_id: string;
  full_name: string | null;
  binding_revision: string | null;
}
export interface BindingPreset {
  channel_id: string;
  channel_revision: string;
  preset_revision: string;
  mappings: PresetMapping[];
}
export interface BindingTarget {
  charity_model_id: string;
  expected_binding_revision: string;
  upstream_model_id: string;
  native_services?: import('@shared/nativeServices').NativeService[];
}
export interface ManualBindingInput {
  expected_donation_revision: string;
  targets: BindingTarget[];
}
export const MAX_MANUAL_BINDINGS = 256;
export interface PresetSaveInput {
  expected_channel_revision: string;
  expected_preset_revision: string;
  mappings: { upstream_model_id: string; charity_model_id: string }[];
}
export interface PresetApplyInput {
  channel_id: string;
  expected_channel_revision: string;
  expected_preset_revision: string;
  expected_donation_revision: string;
  models: { charity_model_id: string; expected_binding_revision: string }[];
}
export const bindingResultStatuses = [
  'added',
  'already_bound',
  'missing_model',
  'invalid_resource',
  'capacity_exceeded',
] as const;
export type BindingResultStatus = (typeof bindingResultStatuses)[number];
export interface BindingMatrixReceipt {
  items: { donation_key_id: string; results: BindingResultStatus[] }[];
  models: { charity_model_id: string; binding_revision: string }[];
  added_count: string;
}
const base = (role: CharityRole) => (role === 'admin' ? '/admin/api' : '/api/steward');
const donationPath = (role: CharityRole, donationID: string) =>
  `${base(role)}/donations/${decimalID(donationID, 'donation id')}`;
const presetPath = (role: CharityRole, channelID: string) =>
  `${base(role)}/charity-binding-presets/${opaqueID(channelID, 'mch_', 'channel id')}`;
export function normalizeBindingOption(value: unknown): BindingOption {
  const row = record(
    value,
    ['charity_model_id', 'full_name', 'enabled', 'binding_revision'],
    'binding option',
  );
  return {
    charity_model_id: decimalID(row.charity_model_id, 'charity model id'),
    full_name: string(row.full_name, 'charity model name', { min: 1 }),
    enabled: boolean(row.enabled, 'model enabled'),
    binding_revision: decimal(row.binding_revision, 'binding revision'),
  };
}
export function normalizeBindingPreset(value: unknown): BindingPreset {
  const row = record(
    value,
    ['channel_id', 'channel_revision', 'preset_revision', 'mappings'],
    'binding preset',
  );
  return {
    channel_id: opaqueID(row.channel_id, 'mch_', 'channel id'),
    channel_revision: decimal(row.channel_revision, 'channel revision'),
    preset_revision: decimal(row.preset_revision, 'preset revision'),
    mappings: array(row.mappings, 'preset mappings', 16).map((value) => {
      const mapping = record(
        value,
        ['upstream_model_id', 'charity_model_id', 'full_name', 'binding_revision'],
        'preset mapping',
      );
      return {
        upstream_model_id: string(mapping.upstream_model_id, 'upstream model id', { min: 1 }),
        charity_model_id: decimalID(mapping.charity_model_id, 'charity model id'),
        full_name: nullableString(mapping.full_name, 'charity model name'),
        binding_revision: nullableDecimal(mapping.binding_revision, 'binding revision'),
      };
    }),
  };
}
export function normalizeBindingReceipt(
  value: unknown,
  columns: number,
  keyID?: string,
): BindingMatrixReceipt {
  const root = record(value, ['items', 'models', 'added_count'], 'binding results');
  const items = array(root.items, 'binding result keys', 100).map((value) => {
    const row = record(value, ['donation_key_id', 'results'], 'binding result key');
    const results = array(row.results, 'binding statuses', MAX_MANUAL_BINDINGS).map((status) =>
      oneOf(status, bindingResultStatuses, 'binding status'),
    );
    if (results.length !== columns) invalidResponse('binding result columns');
    return { donation_key_id: decimalID(row.donation_key_id, 'donation key id'), results };
  });
  if (
    new Set(items.map((row) => row.donation_key_id)).size !== items.length ||
    (keyID && (items.length !== 1 || items[0]?.donation_key_id !== keyID))
  )
    invalidResponse('binding result identity');
  const models = array(root.models, 'binding result models', MAX_MANUAL_BINDINGS).map((value) => {
    const row = record(value, ['charity_model_id', 'binding_revision'], 'binding result model');
    return {
      charity_model_id: decimalID(row.charity_model_id, 'charity model id'),
      binding_revision: decimal(row.binding_revision, 'binding revision'),
    };
  });
  const added_count = decimal(root.added_count, 'added count');
  if (
    BigInt(added_count) !==
    BigInt(items.flatMap((row) => row.results).filter((status) => status === 'added').length)
  )
    invalidResponse('binding added count');
  return { items, models, added_count };
}
export function getBindingOptions(
  role: CharityRole,
  donationID: string,
  keyID: string,
  q: string,
  page: string,
  size: PageSize,
  signal?: AbortSignal,
) {
  validateWindow(page, size);
  return decoded(
    queryPath(
      `${donationPath(role, donationID)}/keys/${decimalID(keyID, 'donation key id')}/binding-options`,
      { q, page, page_size: size },
    ),
    (value) =>
      normalizeNumberedPage(
        value,
        'binding options',
        normalizeBindingOption,
        page,
        size,
        (row) => row.charity_model_id,
      ),
    { signal },
  );
}
export function getPresetChannels(
  role: CharityRole,
  q: string,
  state: 'active' | 'retired' | 'all',
  page: string,
  size: PageSize,
  signal?: AbortSignal,
  donationId?: string,
) {
  validateWindow(page, size);
  return decoded(
    queryPath(`${base(role)}/charity-binding-presets`, {
      q,
      state,
      page,
      page_size: size,
      donation_id: donationId,
    }),
    (value) =>
      normalizeNumberedPage(
        value,
        'preset channels',
        (value): PresetChannel => {
          const required = [
            'channel_id',
            'name',
            'connector_type',
            'base_url',
            'enabled',
            'state',
            'channel_revision',
            'preset_revision',
            'mapping_count',
          ];
          const row = record(value, [...required, 'recommended'], 'preset channel', required);
          return {
            channel_id: opaqueID(row.channel_id, 'mch_', 'channel id'),
            name: string(row.name, 'channel name', { min: 1 }),
            connector_type: string(row.connector_type, 'connector type', { min: 1 }),
            base_url: string(row.base_url, 'channel URL', { min: 1 }),
            enabled: boolean(row.enabled, 'channel enabled'),
            state: oneOf(row.state, ['active', 'retired'] as const, 'channel state'),
            channel_revision: decimal(row.channel_revision, 'channel revision'),
            preset_revision: decimal(row.preset_revision, 'preset revision'),
            mapping_count: decimal(row.mapping_count, 'mapping count'),
            recommended:
              row.recommended === undefined
                ? undefined
                : boolean(row.recommended, 'recommended preset'),
          };
        },
        page,
        size,
        (row) => row.channel_id,
      ),
    { signal },
  );
}
export function getBindingPreset(role: CharityRole, channelID: string, signal?: AbortSignal) {
  return decoded(presetPath(role, channelID), normalizeBindingPreset, { signal });
}
export function saveBindingPreset(
  role: CharityRole,
  channelID: string,
  input: PresetSaveInput,
  key: string,
  signal?: AbortSignal,
) {
  return decoded(
    presetPath(role, channelID),
    normalizeBindingPreset,
    idempotentOptions(key, { method: 'PUT', json: input, signal }),
  );
}
export function addKeyBindings(
  role: CharityRole,
  donationID: string,
  keyID: string,
  input: ManualBindingInput,
  key: string,
  signal?: AbortSignal,
) {
  return decoded(
    `${donationPath(role, donationID)}/keys/${decimalID(keyID, 'donation key id')}/bindings/batch`,
    (value) => normalizeBindingReceipt(value, input.targets.length, keyID),
    idempotentOptions(key, { method: 'POST', json: input, signal }),
  );
}
export function applyBindingPreset(
  role: CharityRole,
  donationID: string,
  input: PresetApplyInput,
  mappingCount: number,
  key: string,
  signal?: AbortSignal,
) {
  return decoded(
    `${donationPath(role, donationID)}/bindings/apply-preset`,
    (value) => normalizeBindingReceipt(value, mappingCount),
    idempotentOptions(key, { method: 'POST', json: input, signal }),
  );
}
