import { ApiError } from '@shared/query/http';
import { coreRequest } from './request';
import { validateIdempotencyKey } from './normalizers';
import {
  addBindings,
  createEndpoint,
  createEndpointKey,
  createManualEntries,
  createModel,
  getBindings,
  getCatalog,
  getEndpoint,
  getModel,
  listEndpointKeys,
  refreshDiscovery,
} from './api';
import type {
  BindingSelection,
  BindingsResponse,
  CatalogView,
  DiscoveryAccepted,
  Endpoint,
  EndpointCreateInput,
  EndpointKey,
  EndpointKeyCreateInput,
  Model,
  ModelCreateInput,
} from './types';

export type ResourceIntent =
  | { kind: 'endpoint'; input: EndpointCreateInput }
  | { kind: 'key'; endpointId: string; input: Omit<EndpointKeyCreateInput, 'secret'> }
  | { kind: 'refresh'; endpointId: string; keyId: string }
  | {
      kind: 'manual';
      endpointId: string;
      keyId: string;
      entries: { upstream_model_id: string; provider: string }[];
    }
  | { kind: 'model'; row: string; input: ModelCreateInput }
  | {
      kind: 'binding';
      row: string;
      modelId: string;
      revision: string;
      selection: BindingSelection;
    };

export type ResourceResult =
  | { kind: 'endpoint'; endpoint: Endpoint }
  | { kind: 'key'; endpointId: string; keyId: string; key: EndpointKey }
  | { kind: 'refresh'; accepted?: DiscoveryAccepted; catalog: CatalogView }
  | { kind: 'manual'; catalog: CatalogView }
  | { kind: 'model'; row: string; model: Model }
  | { kind: 'binding'; row: string; model: Model; bindings: BindingsResponse };

type Stage = 'endpoint' | 'key' | 'catalog_refresh' | 'catalog_manual' | 'model' | 'binding_batch';
export type ResourceStatus =
  | { status: 'not_recorded' | 'expired' }
  | { status: 'recorded' | 'in_progress'; stage: Stage; result: Record<string, string | string[]> };

function invalid(): never {
  throw new ApiError('invalid_response', 'The operation status does not match this action.', 200);
}

export async function resourceStatus(key: string, signal?: AbortSignal): Promise<ResourceStatus> {
  const response = await coreRequest('/api/resource-operation-status', {
    method: 'POST',
    json: { operation_key: validateIdempotencyKey(key) },
    signal,
  });
  if (response.status !== 200) invalid();
  return response.payload as ResourceStatus;
}

export async function readEndpointKey(
  endpointId: string,
  keyId: string,
  signal: AbortSignal,
): Promise<EndpointKey> {
  let cursor: string | undefined;
  do {
    const page = await listEndpointKeys(endpointId, cursor, signal);
    const key = page.data.find((item) => item.id === keyId);
    if (key) return key;
    cursor = page.next_cursor ?? undefined;
  } while (cursor);
  throw new ApiError('not_found', 'The saved service key is no longer present.', 404);
}

export async function executeResource(
  intent: ResourceIntent,
  secret: string | undefined,
  key: string,
  signal: AbortSignal,
): Promise<ResourceResult> {
  const identity = { idempotencyKey: key, actionId: key };
  switch (intent.kind) {
    case 'endpoint':
      return { kind: 'endpoint', endpoint: await createEndpoint(intent.input, identity, signal) };
    case 'key': {
      if (!secret) throw new ApiError('secret_required', 'Enter the service key again.', 400);
      const saved = await createEndpointKey(
        intent.endpointId,
        { ...intent.input, secret },
        identity,
        signal,
      );
      return { kind: 'key', endpointId: saved.endpoint_id, keyId: saved.id, key: saved };
    }
    case 'refresh': {
      const accepted = await refreshDiscovery(intent.endpointId, intent.keyId, identity, signal);
      return {
        kind: 'refresh',
        accepted,
        catalog: await getCatalog(intent.endpointId, intent.keyId, undefined, signal),
      };
    }
    case 'manual': {
      await createManualEntries(intent.endpointId, intent.keyId, intent.entries, identity, signal);
      return {
        kind: 'manual',
        catalog: await getCatalog(intent.endpointId, intent.keyId, undefined, signal),
      };
    }
    case 'model':
      return {
        kind: 'model',
        row: intent.row,
        model: await createModel(intent.input, identity, signal),
      };
    case 'binding': {
      const bindings = await addBindings(
        intent.modelId,
        intent.revision,
        [intent.selection],
        identity,
        signal,
      );
      return {
        kind: 'binding',
        row: intent.row,
        model: await getModel(intent.modelId, signal),
        bindings,
      };
    }
  }
}

export async function readResourceResult(
  intent: ResourceIntent,
  status: ResourceStatus,
  signal: AbortSignal,
): Promise<ResourceResult | null> {
  if (!('stage' in status)) return null;
  const expected: Stage =
    intent.kind === 'refresh'
      ? 'catalog_refresh'
      : intent.kind === 'manual'
        ? 'catalog_manual'
        : intent.kind === 'binding'
          ? 'binding_batch'
          : intent.kind;
  if (status.stage !== expected) invalid();
  const id = (field: string) => {
    const value = status.result[field];
    return value as string;
  };
  if ('endpointId' in intent && id('endpoint_id') !== intent.endpointId) invalid();
  if ('keyId' in intent && id('endpoint_key_id') !== intent.keyId) invalid();
  switch (intent.kind) {
    case 'endpoint':
      return { kind: 'endpoint', endpoint: await getEndpoint(id('endpoint_id'), signal) };
    case 'key': {
      const keyId = id('endpoint_key_id');
      const key = await readEndpointKey(intent.endpointId, keyId, signal);
      return { kind: 'key', endpointId: intent.endpointId, keyId, key };
    }
    case 'refresh':
    case 'manual':
      return {
        kind: intent.kind,
        catalog: await getCatalog(intent.endpointId, intent.keyId, undefined, signal),
      };
    case 'model':
      return { kind: 'model', row: intent.row, model: await getModel(id('model_id'), signal) };
    case 'binding': {
      if (id('model_id') !== intent.modelId) invalid();
      const [model, bindings] = await Promise.all([
        getModel(intent.modelId, signal),
        getBindings(intent.modelId, signal),
      ]);
      if (
        !bindings.bindings.some(
          (b) =>
            b.endpoint_key_id === intent.selection.endpoint_key_id &&
            b.upstream_model_id === intent.selection.upstream_model_id,
        )
      )
        throw new ApiError('conflict', 'The connection is no longer present.', 409);
      return { kind: 'binding', row: intent.row, model, bindings };
    }
  }
}
