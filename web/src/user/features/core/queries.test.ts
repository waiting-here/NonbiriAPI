import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { CancelledError, QueryClient } from '@tanstack/react-query';
import { describe, expect, it, vi } from 'vitest';
import { canonicalCandidateFilters } from './normalizers';
import { userKeys } from '../../data';
import {
  applyBindingsResponse,
  applyManualUpdateToCache,
  clearCoreAccountQueries,
  clearCoreUserSession,
  coreSessionAccountId,
  coreKeys,
  fetchCoreSession,
  invalidateResourceDependents,
  normalizeCoreSessionBoundary,
  updateCoreSessionLanguage,
} from './queries';
import type { Binding, Model, Page } from './types';

const model = (id: string, revision = '0'): Model => ({
  id,
  provider: 'provider',
  model: `model-${id}`,
  full_name: `provider/model-${id}`,
  route_strategy: 'ordered',
  silent_retry: false,
  flatten_tool_calls: false,
  revision: '1',
  binding_revision: revision,
  binding_count: '0',
  created_at: 1_700_000_000,
  updated_at: 1_700_000_000,
});

const binding = (id: string): Binding => ({
  id,
  endpoint_key_id: '21',
  endpoint_base_url: 'https://example.com/v1',
  connector_type: 'openai-compatible',
  endpoint_note: '',
  endpoint_key_display_head: 'head',
  endpoint_key_display_tail: 'tail',
  endpoint_key_note: '',
  upstream_model_id: 'Vendor/Exact',
  ord: 0,
});

function deferred<T>() {
  let resolvePromise!: (value: T) => void;
  const promise = new Promise<T>((resolveValue) => {
    resolvePromise = resolveValue;
  });
  return { promise, resolve: resolvePromise };
}

describe('core query identity and cache projections', () => {
  it('includes the account boundary and canonical filter identity in every private key', () => {
    expect(coreKeys.session).toEqual(['user', 'session']);
    expect(coreKeys.endpoint('account-a', '11')).not.toEqual(coreKeys.endpoint('account-b', '11'));
    expect(coreKeys.callerKey('account-a')).not.toEqual(coreKeys.callerKey('account-b'));
    expect(coreKeys.home('account-a', 'checkin')).not.toEqual(
      coreKeys.home('account-b', 'checkin'),
    );
    expect(coreKeys.home('account-a', 'games')).not.toEqual(
      coreKeys.home('account-a', 'announcements'),
    );
    expect(
      coreKeys.candidates('account-a', '31', canonicalCandidateFilters({ keyId: '21' })),
    ).toEqual(
      coreKeys.candidates('account-a', '31', canonicalCandidateFilters({ keyId: '21', limit: 50 })),
    );
  });

  it('reads only a canonical exact identity from either shared session projection', () => {
    const largestId = '9223372036854775807';
    expect(coreSessionAccountId({ user: { id: largestId } })).toBe(largestId);
    expect(normalizeCoreSessionBoundary({ user: { id: largestId, legacy_marker: true } })).toEqual({
      accountId: largestId,
    });
    expect(normalizeCoreSessionBoundary(null)).toBeNull();
    expect(() => coreSessionAccountId({ user: { id: '01' } })).toThrow(/invalid session identity/i);
    expect(() => coreSessionAccountId({ user: { id: 7 } })).toThrow(/invalid session identity/i);
  });

  it('patches language without replacing the shell-owned session shape or a newer identity', () => {
    const client = new QueryClient();
    const shared = {
      user: {
        id: '9223372036854775807',
        username: 'legacy-shell-user',
        effective_level: 2,
        lang: 'en',
        legacy_marker: 'preserve-me',
      },
    };
    client.setQueryData(coreKeys.session, shared);

    expect(updateCoreSessionLanguage(client, shared.user.id, 'zh')).toBe(true);
    expect(client.getQueryData(coreKeys.session)).toEqual({
      user: { ...shared.user, lang: 'zh' },
    });

    const next = { user: { id: '8', lang: 'en', marker: 'new-account' } };
    client.setQueryData(coreKeys.session, next);
    expect(updateCoreSessionLanguage(client, shared.user.id, 'zh')).toBe(false);
    expect(client.getQueryData(coreKeys.session)).toEqual(next);
  });

  it('closes the shared session with a null sentinel and evicts all user cache and mutations', () => {
    const client = new QueryClient();
    client.setQueryData(coreKeys.session, { user: { id: '7' } });
    client.setQueryData(coreKeys.me('7'), { private: 'profile' });
    client.setQueryData(['user', 'legacy-private'], { private: 'legacy' });
    client.setQueryData(['admin', 'session'], { admin: { username: 'root' } });
    client.getMutationCache().build(client, {
      mutationKey: ['user', 'sensitive-mutation'],
      mutationFn: async () => undefined,
    });

    clearCoreUserSession(client);

    expect(client.getQueryData(coreKeys.session)).toBeNull();
    expect(client.getQueryData(coreKeys.me('7'))).toBeUndefined();
    expect(client.getQueryData(['user', 'legacy-private'])).toBeUndefined();
    expect(client.getQueryData(['admin', 'session'])).toEqual({ admin: { username: 'root' } });
    expect(client.getMutationCache().getAll()).toHaveLength(0);
  });

  it('does not let a late session response replace the logout null sentinel', async () => {
    const client = new QueryClient();
    client.setQueryData(coreKeys.session, { user: { id: '7' } });
    const response = deferred<Response>();
    const fetchMock = vi.fn(() => response.promise);
    vi.stubGlobal('fetch', fetchMock);
    const pending = fetchCoreSession(client);
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));

    clearCoreUserSession(client);
    response.resolve(
      new Response(
        readFileSync(
          resolve(process.cwd(), '..', 'internal/auth/testdata/user_envelope.json'),
          'utf8',
        ),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    );

    await expect(pending).rejects.toBeInstanceOf(CancelledError);
    expect(client.getQueryData(coreKeys.session)).toBeNull();
  });

  it('updates binding detail and every model page atomically within one account only', () => {
    const client = new QueryClient();
    client.setQueryData(coreKeys.session, { user: { id: '1' } });
    const originalA = model('31');
    const originalB = model('31');
    const pageA: Page<Model> = { data: [originalA], next_cursor: null };
    const pageB: Page<Model> = { data: [originalB], next_cursor: null };
    client.setQueryData(coreKeys.model('1', '31'), originalA);
    client.setQueryData(coreKeys.models('1'), pageA);
    client.setQueryData(coreKeys.model('2', '31'), originalB);
    client.setQueryData(coreKeys.models('2'), pageB);

    applyBindingsResponse(client, '1', '31', {
      bindings: [binding('51')],
      binding_revision: '1',
    });

    expect(client.getQueryData<Model>(coreKeys.model('1', '31'))).toMatchObject({
      binding_revision: '1',
      binding_count: '1',
    });
    expect(client.getQueryData<Page<Model>>(coreKeys.models('1'))?.data[0]).toMatchObject({
      binding_revision: '1',
      binding_count: '1',
    });
    expect(client.getQueryData<Model>(coreKeys.model('2', '31'))).toEqual(originalB);
  });

  it('applies affected manual-entry models as one complete projection', () => {
    const client = new QueryClient();
    client.setQueryData(coreKeys.session, { user: { id: '1' } });
    client.setQueryData(coreKeys.models('1'), { data: [model('31')], next_cursor: null });
    const updated = { ...model('31', '2'), binding_count: '1' };
    applyManualUpdateToCache(client, '1', {
      entries: [],
      affected_models: [{ model: updated, bindings: [binding('51')] }],
    });
    expect(client.getQueryData(coreKeys.model('1', '31'))).toEqual(updated);
    expect(client.getQueryData(coreKeys.bindings('1', '31'))).toEqual({
      bindings: [binding('51')],
      binding_revision: '2',
    });
  });

  it('invalidates model, routing, and charity dependents after a physical key deletion', async () => {
    const client = new QueryClient();
    client.setQueryData(coreKeys.session, { user: { id: '1' } });
    const affectedKeys = [
      coreKeys.models('1'),
      coreKeys.model('1', '31'),
      coreKeys.bindings('1', '31'),
      coreKeys.candidatesRoot('1', '31'),
      coreKeys.endpointRouting('1', '11', ['21']),
      userKeys.endpoints,
      userKeys.endpointKeys('11'),
      userKeys.keyModels('11', '21'),
      userKeys.models,
      userKeys.bindings('31'),
      userKeys.charityModels,
      userKeys.donations,
    ] as const;
    for (const key of affectedKeys) client.setQueryData(key, { marker: 'stale' });
    client.setQueryData(coreKeys.model('2', '31'), { marker: 'other-account' });

    expect(
      await invalidateResourceDependents(client, '1', {
        endpointId: '11',
        modelIds: ['31'],
        charity: true,
      }),
    ).toBe(true);

    for (const key of affectedKeys) {
      expect(client.getQueryState(key)?.isInvalidated, JSON.stringify(key)).toBe(true);
    }
    expect(client.getQueryState(coreKeys.model('2', '31'))?.isInvalidated).toBe(false);
  });

  it('rejects mutation cache projections after the shared session changes account', () => {
    const client = new QueryClient();
    client.setQueryData(coreKeys.session, { user: { id: '2' } });

    expect(
      applyBindingsResponse(client, '1', '31', {
        bindings: [binding('51')],
        binding_revision: '1',
      }),
    ).toBe(false);
    expect(
      applyManualUpdateToCache(client, '1', {
        entries: [],
        affected_models: [{ model: model('31'), bindings: [binding('51')] }],
      }),
    ).toBe(false);
    expect(client.getQueryData(coreKeys.model('1', '31'))).toBeUndefined();
    expect(client.getQueryData(coreKeys.bindings('1', '31'))).toBeUndefined();
  });

  it('removes only the selected account namespace', () => {
    const client = new QueryClient();
    client.setQueryData(coreKeys.me('account-a'), { marker: 'a' });
    client.setQueryData(coreKeys.me('account-b'), { marker: 'b' });
    client.setQueryData(coreKeys.home('account-a', 'checkin'), { enabled: false });
    client.setQueryData(coreKeys.home('account-b', 'checkin'), { enabled: false });
    clearCoreAccountQueries(client, 'account-a');
    expect(client.getQueryData(coreKeys.me('account-a'))).toBeUndefined();
    expect(client.getQueryData(coreKeys.me('account-b'))).toEqual({ marker: 'b' });
    expect(client.getQueryData(coreKeys.home('account-a', 'checkin'))).toBeUndefined();
    expect(client.getQueryData(coreKeys.home('account-b', 'checkin'))).toEqual({
      enabled: false,
    });
  });
});
