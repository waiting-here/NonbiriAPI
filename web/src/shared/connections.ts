import type { ModelType } from './modelTypes';
import { MODEL_TYPES } from './modelTypes';
import {
  NATIVE_SERVICES,
  RERANK_FORMATS,
  type NativeService,
  type RerankFormat,
} from './nativeServices';
import { array, boolean, decimal, nullableString, oneOf, record, string } from './operations/wire';

export interface ServiceRoute {
  operation: ModelType;
  native_service: NativeService;
  mode: 'native' | 'converted';
}
export interface BindingCapabilities {
  can_bind: boolean;
  unavailable_reason: string | null;
}
export interface ServiceProjection {
  native_services: NativeService[];
  model_types: ModelType[];
  service_routes: ServiceRoute[];
  capabilities: BindingCapabilities;
}
export interface NativeServiceOption {
  connector_type: string;
  configuration_type: string;
  native_services: NativeService[];
  default_native_services: NativeService[];
  rerank_formats: RerankFormat[];
}
export interface MainstreamEntry {
  id?: string;
  name: string;
  connector_type: string;
  base_url: string;
  native_services: NativeService[];
  rerank_format: RerankFormat | '';
  enabled: boolean;
}
export const DISCOVERY_FORMATS = [
  'openai',
  'anthropic',
  'gemini',
  'ai-sdk-gateway-v3',
  'ai-sdk-gateway-v4',
] as const;
export type DiscoveryFormat = (typeof DISCOVERY_FORMATS)[number];
export interface DiscoveryPreset {
  base_url: string;
  list_format: DiscoveryFormat;
}
export interface ModelMapping {
  model_id: string;
  entries: { entry_id: string; upstream_model_id: string; native_services: NativeService[] }[];
}

export function normalizeNativeServices(value: unknown): NativeService[] {
  return array(value, 'native services', NATIVE_SERVICES.length).map((item) =>
    oneOf(item, NATIVE_SERVICES, 'native service'),
  );
}
export function normalizeRerankFormat(value: unknown): RerankFormat | '' {
  return value === '' ? '' : oneOf(value, RERANK_FORMATS, 'rerank format');
}
export function normalizeServiceProjection(value: unknown): ServiceProjection {
  const source = record(
    value,
    ['native_services', 'model_types', 'service_routes', 'capabilities'],
    'service projection',
  );
  const capabilities = record(
    source.capabilities,
    ['can_bind', 'unavailable_reason'],
    'binding capabilities',
  );
  return {
    native_services: normalizeNativeServices(source.native_services),
    model_types: array(source.model_types, 'provided services', MODEL_TYPES.length).map((item) =>
      oneOf(item, MODEL_TYPES, 'provided service'),
    ),
    service_routes: array(source.service_routes, 'service routes').map((item) => {
      const route = record(item, ['operation', 'native_service', 'mode'], 'service route');
      return {
        operation: oneOf(route.operation, MODEL_TYPES, 'operation'),
        native_service: oneOf(route.native_service, NATIVE_SERVICES, 'native service'),
        mode: oneOf(route.mode, ['native', 'converted'] as const, 'service route mode'),
      };
    }),
    capabilities: {
      can_bind: boolean(capabilities.can_bind, 'can bind'),
      unavailable_reason: nullableString(
        capabilities.unavailable_reason,
        'binding unavailable reason',
      ),
    },
  };
}
export function normalizeNativeServiceOption(value: unknown): NativeServiceOption {
  const source = record(
    value,
    [
      'connector_type',
      'configuration_type',
      'native_services',
      'default_native_services',
      'rerank_formats',
    ],
    'native service options',
  );
  const text = (key: string) => string(source[key], 'native service option', { min: 1 });
  return {
    connector_type: text('connector_type'),
    configuration_type: text('configuration_type'),
    native_services: normalizeNativeServices(source.native_services),
    default_native_services: normalizeNativeServices(source.default_native_services),
    rerank_formats: array(source.rerank_formats, 'rerank formats', RERANK_FORMATS.length).map(
      (item) => oneOf(item, RERANK_FORMATS, 'rerank format'),
    ),
  };
}
export function normalizeMainstreamEntry(value: unknown): MainstreamEntry {
  const source = record(
    value,
    ['id', 'name', 'connector_type', 'base_url', 'native_services', 'rerank_format', 'enabled'],
    'mainstream entry',
    ['name', 'connector_type', 'base_url', 'native_services', 'rerank_format', 'enabled'],
  );
  return {
    ...(source.id === undefined ? {} : { id: decimal(source.id, 'entry id', { positive: true }) }),
    name: string(source.name, 'entry name'),
    connector_type: string(source.connector_type, 'entry connector', { min: 1 }),
    base_url: string(source.base_url, 'entry URL', { min: 1 }),
    native_services: normalizeNativeServices(source.native_services),
    rerank_format: normalizeRerankFormat(source.rerank_format),
    enabled: boolean(source.enabled, 'entry enabled'),
  };
}

export function normalizeDiscoveryPreset(value: unknown): DiscoveryPreset | null {
  if (value === null) return null;
  const source = record(value, ['base_url', 'list_format'], 'discovery preset');
  return {
    base_url: string(source.base_url, 'discovery URL', { min: 1 }),
    list_format: oneOf(source.list_format, DISCOVERY_FORMATS, 'discovery format'),
  };
}
export function normalizeModelMappings(value: unknown): ModelMapping[] {
  return array(value, 'model mappings', 1024).map((item) => {
    const source = record(item, ['model_id', 'entries'], 'model mapping');
    return {
      model_id: string(source.model_id, 'mapped model', { min: 1 }),
      entries: array(source.entries, 'mapped entries', 32).map((item) => {
        const target = record(
          item,
          ['entry_id', 'upstream_model_id', 'native_services'],
          'mapped entry',
        );
        return {
          entry_id: decimal(target.entry_id, 'entry id', { positive: true }),
          upstream_model_id: string(target.upstream_model_id, 'mapped upstream model', { min: 1 }),
          native_services: normalizeNativeServices(target.native_services),
        };
      }),
    };
  });
}
