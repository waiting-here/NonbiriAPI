import { CancelledError, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query';
import { clearManagementSession, clearStationSession } from '@shared/charityManagement';
import { ApiError } from '@shared/query/http';
import { userKeys } from '../../data';
import {
  collectAllModels,
  getBindings,
  getBindingCandidates,
  getCallerKey,
  getCatalog,
  getEndpoint,
  getMe,
  getModel,
  getSession,
  listEndpointKeys,
  listEndpoints,
  listModels,
} from './api';
import {
  canonicalCandidateFilters,
  normalizeUserEnvelope,
  validateResourceId,
  type CanonicalCandidateFilters,
} from './normalizers';
import type {
  BindingsResponse,
  CandidateFilters,
  ExplicitLanguage,
  ManualUpdateResponse,
  Model,
  Page,
  UserProfile,
} from './types';

export const coreKeys = {
  all: ['user', 'core'] as const,
  session: ['user', 'session'] as const,
  account: (accountId: string) => ['user', 'core', 'account', accountId] as const,
  me: (accountId: string) => ['user', 'core', 'account', accountId, 'me'] as const,
  endpointsRoot: (accountId: string) =>
    ['user', 'core', 'account', accountId, 'endpoints'] as const,
  endpoints: (accountId: string, cursor?: string) =>
    ['user', 'core', 'account', accountId, 'endpoints', cursor ?? 'first'] as const,
  endpoint: (accountId: string, endpointId: string) =>
    ['user', 'core', 'account', accountId, 'endpoint', endpointId] as const,
  endpointKeysRoot: (accountId: string, endpointId: string) =>
    ['user', 'core', 'account', accountId, 'endpoint-keys', endpointId] as const,
  endpointKeys: (accountId: string, endpointId: string, cursor?: string) =>
    ['user', 'core', 'account', accountId, 'endpoint-keys', endpointId, cursor ?? 'first'] as const,
  catalogRoot: (accountId: string, endpointId: string, keyId: string) =>
    ['user', 'core', 'account', accountId, 'catalog', endpointId, keyId] as const,
  catalog: (accountId: string, endpointId: string, keyId: string, cursor?: string) =>
    [
      'user',
      'core',
      'account',
      accountId,
      'catalog',
      endpointId,
      keyId,
      cursor ?? 'first',
    ] as const,
  modelsRoot: (accountId: string) => ['user', 'core', 'account', accountId, 'models'] as const,
  models: (accountId: string, cursor?: string) =>
    ['user', 'core', 'account', accountId, 'models', cursor ?? 'first'] as const,
  model: (accountId: string, modelId: string) =>
    ['user', 'core', 'account', accountId, 'model', modelId] as const,
  bindings: (accountId: string, modelId: string) =>
    ['user', 'core', 'account', accountId, 'bindings', modelId] as const,
  candidatesRoot: (accountId: string, modelId: string) =>
    ['user', 'core', 'account', accountId, 'binding-candidates', modelId] as const,
  candidates: (accountId: string, modelId: string, filters: CanonicalCandidateFilters) =>
    [
      'user',
      'core',
      'account',
      accountId,
      'binding-candidates',
      modelId,
      filters.endpointId ?? '',
      filters.keyId ?? '',
      filters.source ?? '',
      filters.query ?? '',
      filters.cursor ?? '',
      filters.limit ?? 50,
    ] as const,
  callerKey: (accountId: string) => ['user', 'core', 'account', accountId, 'caller-key'] as const,
  endpointRoutingRoot: (accountId: string, endpointId: string) =>
    ['user', 'core', 'account', accountId, 'endpoint-routing', endpointId] as const,
  endpointRoutingAll: (accountId: string) =>
    ['user', 'core', 'account', accountId, 'endpoint-routing'] as const,
  endpointRouting: (accountId: string, endpointId: string, keyIds: readonly string[]) =>
    [
      'user',
      'core',
      'account',
      accountId,
      'endpoint-routing',
      endpointId,
      ...[...keyIds].sort(),
    ] as const,
  home: (accountId: string, capability: 'games' | 'announcements') =>
    ['user', 'core', 'account', accountId, 'home', capability] as const,
};

export interface CoreSessionBoundary {
  accountId: string;
  /** Present only when the shared cache already contains the strict beta.1 envelope. */
  profile?: UserProfile;
}

export function coreSessionAccountId(value: unknown): string | null {
  if (value === null) return null;
  if (value === undefined || typeof value !== 'object' || Array.isArray(value)) {
    throw new ApiError('invalid_response', 'The server returned an invalid session identity.', 200);
  }
  const user = (value as Record<string, unknown>).user;
  if (user === null || typeof user !== 'object' || Array.isArray(user)) {
    throw new ApiError('invalid_response', 'The server returned an invalid session identity.', 200);
  }
  try {
    return validateResourceId((user as Record<string, unknown>).id as string, 'user id');
  } catch {
    throw new ApiError('invalid_response', 'The server returned an invalid session identity.', 200);
  }
}

export function normalizeCoreSessionBoundary(value: unknown): CoreSessionBoundary | null {
  const accountId = coreSessionAccountId(value);
  if (accountId === null) return null;
  try {
    const profile = normalizeUserEnvelope(value).user;
    return { accountId, profile };
  } catch {
    // The existing shell still owns a narrower session projection. H1 only
    // consumes its identity and obtains the strict profile from GET /api/me.
    return { accountId };
  }
}

export function coreSessionMatchesAccount(queryClient: QueryClient, accountId: string): boolean {
  try {
    return coreSessionAccountId(queryClient.getQueryData(coreKeys.session)) === accountId;
  } catch {
    return false;
  }
}

export function currentCoreSessionLanguage(queryClient: QueryClient): ExplicitLanguage | null {
  const current = queryClient.getQueryData(coreKeys.session);
  if (current === null || typeof current !== 'object') return null;
  const user = (current as Record<string, unknown>).user;
  if (user === null || typeof user !== 'object' || Array.isArray(user)) return null;
  const language = (user as Record<string, unknown>).lang;
  return language === 'zh' || language === 'en' ? language : null;
}

export function coreSessionLanguage(
  queryClient: QueryClient,
  accountId: string,
): ExplicitLanguage | null {
  return coreSessionMatchesAccount(queryClient, accountId)
    ? currentCoreSessionLanguage(queryClient)
    : null;
}

export function updateCoreSessionLanguage(
  queryClient: QueryClient,
  accountId: string,
  language: ExplicitLanguage,
): boolean {
  const current = queryClient.getQueryData(coreKeys.session);
  if (
    !coreSessionMatchesAccount(queryClient, accountId) ||
    current === null ||
    typeof current !== 'object'
  ) {
    return false;
  }
  const record = current as Record<string, unknown>;
  const user = record.user as Record<string, unknown>;
  queryClient.setQueryData(coreKeys.session, {
    ...record,
    user: { ...user, lang: language },
  });
  return true;
}

export async function fetchCoreSession(
  queryClient: QueryClient,
  signal?: AbortSignal,
): Promise<unknown> {
  const initial = queryClient.getQueryData(coreKeys.session);
  const initialBoundary = initial === undefined ? undefined : normalizeCoreSessionBoundary(initial);
  const envelope = await getSession(signal);
  const current = queryClient.getQueryData(coreKeys.session);
  if (current === null || (current === undefined && initial !== undefined))
    throw new CancelledError();
  if (current !== undefined) {
    const currentBoundary = normalizeCoreSessionBoundary(current);
    if (
      currentBoundary === null ||
      initialBoundary === null ||
      (initialBoundary && currentBoundary.accountId !== initialBoundary.accountId) ||
      (initialBoundary === undefined && currentBoundary.accountId !== envelope.user.id)
    ) {
      throw new CancelledError();
    }
  }
  if (initialBoundary && initialBoundary.accountId !== envelope.user.id) {
    clearStationSession(queryClient, 'steward', true);
  }
  return envelope;
}

export function useCoreSession(enabled = true) {
  const queryClient = useQueryClient();
  return useQuery<unknown, Error, CoreSessionBoundary | null>({
    queryKey: coreKeys.session,
    queryFn: ({ signal }) => fetchCoreSession(queryClient, signal),
    select: normalizeCoreSessionBoundary,
    enabled,
    staleTime: 15_000,
    retry: false,
  });
}

export function useCoreMe(accountId: string, enabled = true) {
  return useQuery({
    queryKey: coreKeys.me(accountId),
    queryFn: async ({ signal }) => {
      const envelope = await getMe(signal);
      if (envelope.user.id !== accountId) {
        throw new ApiError(
          'invalid_response',
          'The server returned a different user profile.',
          200,
        );
      }
      return envelope;
    },
    enabled,
    staleTime: 10_000,
    retry: false,
  });
}

export function useEndpointsPage(accountId: string, cursor?: string, enabled = true) {
  return useQuery({
    queryKey: coreKeys.endpoints(accountId, cursor),
    queryFn: ({ signal }) => listEndpoints(cursor, signal),
    enabled,
  });
}

export function useEndpoint(accountId: string, endpointId: string | undefined, enabled = true) {
  return useQuery({
    queryKey: endpointId
      ? coreKeys.endpoint(accountId, endpointId)
      : [...coreKeys.endpointsRoot(accountId), 'none'],
    queryFn: ({ signal }) => {
      if (!endpointId) throw new Error('endpoint id is required');
      return getEndpoint(endpointId, signal);
    },
    enabled: enabled && Boolean(endpointId),
  });
}

export function useEndpointKeysPage(
  accountId: string,
  endpointId: string | undefined,
  cursor?: string,
  enabled = true,
) {
  return useQuery({
    queryKey: endpointId
      ? coreKeys.endpointKeys(accountId, endpointId, cursor)
      : [...coreKeys.endpointsRoot(accountId), 'keys', 'none'],
    queryFn: ({ signal }) => {
      if (!endpointId) throw new Error('endpoint id is required');
      return listEndpointKeys(endpointId, cursor, signal);
    },
    enabled: enabled && Boolean(endpointId),
  });
}

export function useCatalog(
  accountId: string,
  endpointId: string | undefined,
  keyId: string | undefined,
  cursor?: string,
  enabled = true,
) {
  return useQuery({
    queryKey:
      endpointId && keyId
        ? coreKeys.catalog(accountId, endpointId, keyId, cursor)
        : [...coreKeys.endpointsRoot(accountId), 'catalog', 'none'],
    queryFn: ({ signal }) => {
      if (!endpointId || !keyId) throw new Error('endpoint and key ids are required');
      return getCatalog(endpointId, keyId, cursor, signal);
    },
    enabled: enabled && Boolean(endpointId && keyId),
    staleTime: 5_000,
  });
}

export function useModelsPage(accountId: string, cursor?: string, enabled = true) {
  return useQuery({
    queryKey: coreKeys.models(accountId, cursor),
    queryFn: ({ signal }) => listModels(cursor, signal),
    enabled,
  });
}

export function useModel(accountId: string, modelId: string | undefined, enabled = true) {
  return useQuery({
    queryKey: modelId
      ? coreKeys.model(accountId, modelId)
      : [...coreKeys.modelsRoot(accountId), 'none'],
    queryFn: ({ signal }) => {
      if (!modelId) throw new Error('model id is required');
      return getModel(modelId, signal);
    },
    enabled: enabled && Boolean(modelId),
  });
}

export function useBindings(accountId: string, modelId: string | undefined, enabled = true) {
  return useQuery({
    queryKey: modelId
      ? coreKeys.bindings(accountId, modelId)
      : [...coreKeys.modelsRoot(accountId), 'bindings', 'none'],
    queryFn: ({ signal }) => {
      if (!modelId) throw new Error('model id is required');
      return getBindings(modelId, signal);
    },
    enabled: enabled && Boolean(modelId),
  });
}

export function useBindingCandidates(
  accountId: string,
  modelId: string | undefined,
  filters: CandidateFilters,
  enabled = true,
) {
  const canonical = canonicalCandidateFilters(filters);
  return useQuery({
    queryKey: modelId
      ? coreKeys.candidates(accountId, modelId, canonical)
      : [...coreKeys.modelsRoot(accountId), 'candidates', 'none'],
    queryFn: ({ signal }) => {
      if (!modelId) throw new Error('model id is required');
      return getBindingCandidates(
        modelId,
        {
          endpointId: canonical.endpointId || undefined,
          keyId: canonical.keyId || undefined,
          source: canonical.source || undefined,
          query: canonical.query || undefined,
          cursor: canonical.cursor || undefined,
          limit: canonical.limit,
        },
        signal,
      );
    },
    enabled: enabled && Boolean(modelId),
    staleTime: 5_000,
  });
}

export function useCallerKey(accountId: string, enabled = true) {
  return useQuery({
    queryKey: coreKeys.callerKey(accountId),
    queryFn: ({ signal }) => getCallerKey(signal),
    enabled,
    staleTime: 5_000,
    retry: false,
  });
}

export interface EndpointRoutingProjection {
  byKey: Record<
    string,
    Array<{
      model: { id: string; full_name: string };
      binding: BindingsResponse['bindings'][number];
    }>
  >;
}

export function useEndpointRoutingProjection(
  accountId: string,
  endpointId: string | undefined,
  keyIds: readonly string[],
  enabled = true,
) {
  return useQuery({
    queryKey: endpointId
      ? coreKeys.endpointRouting(accountId, endpointId, keyIds)
      : [...coreKeys.endpointsRoot(accountId), 'routing', 'none'],
    queryFn: async ({ signal }): Promise<EndpointRoutingProjection> => {
      const models = await collectAllModels(signal);
      const responses: Array<{ model: Model; bindings: BindingsResponse }> = [];
      for (let offset = 0; offset < models.length; offset += 4) {
        const batch = await Promise.all(
          models.slice(offset, offset + 4).map(async (model) => ({
            model,
            bindings: await getBindings(model.id, signal),
          })),
        );
        responses.push(...batch);
      }
      const expected = new Set(keyIds);
      const byKey: EndpointRoutingProjection['byKey'] = Object.fromEntries(
        keyIds.map((keyId) => [keyId, []]),
      );
      for (const { model, bindings } of responses) {
        for (const binding of bindings.bindings) {
          if (!expected.has(binding.endpoint_key_id)) continue;
          byKey[binding.endpoint_key_id]?.push({
            model: { id: model.id, full_name: model.full_name },
            binding,
          });
        }
      }
      return { byKey };
    },
    enabled: enabled && Boolean(endpointId) && keyIds.length > 0,
    staleTime: 5_000,
  });
}

function replaceModelInPage(page: Page<Model> | undefined, model: Model): Page<Model> | undefined {
  if (!page) return page;
  let found = false;
  const data = page.data.map((candidate) => {
    if (candidate.id !== model.id) return candidate;
    found = true;
    return model;
  });
  return found ? { ...page, data } : page;
}

export function applyBindingsResponse(
  queryClient: QueryClient,
  accountId: string,
  modelId: string,
  response: BindingsResponse,
): boolean {
  if (!coreSessionMatchesAccount(queryClient, accountId)) return false;
  queryClient.setQueryData(coreKeys.bindings(accountId, modelId), response);
  queryClient.setQueryData<Model>(coreKeys.model(accountId, modelId), (current) =>
    current
      ? {
          ...current,
          binding_revision: response.binding_revision,
          binding_count: String(response.bindings.length),
        }
      : current,
  );
  for (const query of queryClient.getQueryCache().findAll({
    queryKey: coreKeys.modelsRoot(accountId),
    exact: false,
  })) {
    const current = query.state.data as Page<Model> | undefined;
    const existing = current?.data.find((model) => model.id === modelId);
    if (!existing) continue;
    queryClient.setQueryData(
      query.queryKey,
      replaceModelInPage(current, {
        ...existing,
        binding_revision: response.binding_revision,
        binding_count: String(response.bindings.length),
      }),
    );
  }
  void queryClient.invalidateQueries({ queryKey: coreKeys.endpointRoutingAll(accountId) });
  return true;
}

export function applyManualUpdateToCache(
  queryClient: QueryClient,
  accountId: string,
  response: ManualUpdateResponse,
): boolean {
  if (!coreSessionMatchesAccount(queryClient, accountId)) return false;
  for (const affected of response.affected_models) {
    queryClient.setQueryData(coreKeys.model(accountId, affected.model.id), affected.model);
    queryClient.setQueryData(coreKeys.bindings(accountId, affected.model.id), {
      bindings: affected.bindings,
      binding_revision: affected.model.binding_revision,
    } satisfies BindingsResponse);
    for (const query of queryClient.getQueryCache().findAll({
      queryKey: coreKeys.modelsRoot(accountId),
      exact: false,
    })) {
      queryClient.setQueryData(
        query.queryKey,
        replaceModelInPage(query.state.data as Page<Model> | undefined, affected.model),
      );
    }
  }
  void queryClient.invalidateQueries({ queryKey: coreKeys.endpointRoutingAll(accountId) });
  return true;
}

export interface ResourceDependentInvalidation {
  endpointId?: string;
  modelIds?: readonly string[] | 'all';
  charity?: boolean;
}

/**
 * Keep resource consumers outside the H1 pages from retaining an authority
 * snapshot after endpoint, key, catalog, or binding topology changes.
 */
export async function invalidateResourceDependents(
  queryClient: QueryClient,
  accountId: string,
  options: ResourceDependentInvalidation = {},
): Promise<boolean> {
  if (!coreSessionMatchesAccount(queryClient, accountId)) return false;

  const invalidations = [
    queryClient.invalidateQueries({ queryKey: userKeys.endpoints }),
    queryClient.invalidateQueries({
      queryKey: options.endpointId
        ? userKeys.endpointKeys(options.endpointId)
        : userKeys.endpointKeysRoot,
    }),
    queryClient.invalidateQueries({ queryKey: userKeys.keyModelsRoot }),
    queryClient.invalidateQueries({
      queryKey: [...coreKeys.account(accountId), 'binding-candidates'],
    }),
    queryClient.invalidateQueries({ queryKey: coreKeys.endpointRoutingAll(accountId) }),
  ];

  if (options.modelIds) {
    invalidations.push(
      queryClient.invalidateQueries({ queryKey: coreKeys.modelsRoot(accountId) }),
      queryClient.invalidateQueries({ queryKey: userKeys.models }),
      queryClient.invalidateQueries({ queryKey: userKeys.bindingsRoot }),
    );
    if (options.modelIds === 'all') {
      invalidations.push(
        queryClient.invalidateQueries({ queryKey: [...coreKeys.account(accountId), 'model'] }),
        queryClient.invalidateQueries({ queryKey: [...coreKeys.account(accountId), 'bindings'] }),
      );
    } else {
      for (const modelId of new Set(options.modelIds)) {
        invalidations.push(
          queryClient.invalidateQueries({ queryKey: coreKeys.model(accountId, modelId) }),
          queryClient.invalidateQueries({ queryKey: coreKeys.bindings(accountId, modelId) }),
          queryClient.invalidateQueries({ queryKey: coreKeys.candidatesRoot(accountId, modelId) }),
        );
      }
    }
  }

  if (options.charity) {
    invalidations.push(
      queryClient.invalidateQueries({ queryKey: userKeys.charityModels }),
      queryClient.invalidateQueries({ queryKey: userKeys.donations }),
    );
  }

  await Promise.all(invalidations);
  return coreSessionMatchesAccount(queryClient, accountId);
}

export function clearCoreAccountQueries(queryClient: QueryClient, accountId: string): void {
  queryClient.removeQueries({ queryKey: coreKeys.account(accountId), exact: false });
}

export function clearCoreUserSession(queryClient: QueryClient): void {
  clearManagementSession(queryClient, 'steward');
}
