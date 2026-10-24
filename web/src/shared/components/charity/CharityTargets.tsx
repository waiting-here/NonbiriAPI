import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { apiFetch } from '@shared/query/http';
import { idempotentOptions, responseOutcomeUnknown } from '@shared/operations/api';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import {
  charityKeys,
  getManagedBindings,
  type CharityBinding,
  type CharityBindingCandidate,
  type CharityModel,
  type CharityRole,
} from '@shared/operations/charity';
import { getManagedBindingCandidatesPage } from '@shared/operations/charityPages';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { PagePagination } from '@shared/operations/PagePagination';
import type { NativeService } from '@shared/nativeServices';
import { NativeServicesField } from '../NativeServicesField';
import { ServiceRoutesSummary } from '../ServiceRoutesSummary';
import { DataTable, Field, Fold, Note } from '../ui';
import { EmptyState, ErrorState, LoadingState } from '../States';

interface Target {
  donation_key_id: string;
  upstream_model_id: string;
  native_services: NativeService[];
  options: NativeService[];
  label: string;
}
export function CharityTargets({
  role,
  accountId,
  model,
  bindingId,
  inline = false,
}: {
  role: CharityRole;
  accountId: string;
  model: CharityModel;
  bindingId?: string;
  inline?: boolean;
}) {
  const { t } = useTranslation();
  const query = useQuery({
    queryKey: [...charityKeys.bindings(role, model.id), accountId],
    queryFn: ({ signal }) => getManagedBindings(role, model.id, signal),
  });
  const binding = bindingId ? query.data?.bindings.find((row) => row.id === bindingId) : undefined;
  const editor = query.error ? (
    <ErrorState error={query.error} onRetry={() => void query.refetch()} />
  ) : !query.data ? (
    <LoadingState />
  ) : bindingId && !binding ? (
    <EmptyState title={t('common.noResults')} body={t('common.noResultsBody')} />
  ) : (
    <TargetEditor
      role={role}
      accountId={accountId}
      model={model}
      binding={binding}
      revision={query.data.binding_revision}
    />
  );
  return inline ? (
    editor
  ) : (
    <Fold title={t('common.connections.addTargetGroup')} lazy>
      {editor}
    </Fold>
  );
}
function TargetEditor({
  role,
  accountId,
  model,
  binding,
  revision,
}: {
  role: CharityRole;
  accountId: string;
  model: CharityModel;
  binding?: CharityBinding;
  revision: string;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const [targets, setTargets] = useState<Target[]>(
    () =>
      binding?.targets?.map((row) => ({
        donation_key_id: row.donation_key_id,
        upstream_model_id: row.upstream_model_id,
        native_services: row.native_services,
        options: row.native_services,
        label: row.base_url ?? row.donation_key_id,
      })) ?? [],
  );
  const [expected, setExpected] = useState(revision);
  const [query, setQuery] = useState('');
  const pager = useUrlPagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: 'charity-target-candidates',
    scopeKey: accountId + ':' + model.id + ':' + (binding?.id ?? 'new'),
    resetKey: query,
    pageParam: 'target_page',
    pageSizeParam: 'target_page_size',
  });
  const candidates = useQuery({
    queryKey: [
      ...charityKeys.root(role),
      accountId,
      'target-candidates',
      model.id,
      query,
      pager.page,
      pager.pageSize,
    ],
    queryFn: ({ signal }) =>
      getManagedBindingCandidatesPage(
        role,
        model.id,
        { q: query },
        pager.page,
        pager.pageSize,
        signal,
      ),
  });
  type Intent = {
    expected_binding_revision: string;
    targets: {
      donation_key_id: string;
      upstream_model_id: string;
      native_services: NativeService[];
    }[];
  };
  const execute = async (input: Intent, key: string, signal: AbortSignal) => {
    const base =
      (role === 'admin' ? '/admin/api' : '/api/steward') +
      '/charity-models/' +
      encodeURIComponent(model.id) +
      '/bindings';
    await apiFetch<unknown>(
      base + (binding ? '/' + encodeURIComponent(binding.id) + '/targets' : '/batch-receipt'),
      idempotentOptions(key, {
        method: binding ? 'PUT' : 'POST',
        json: binding
          ? input
          : {
              expected_binding_revision: input.expected_binding_revision,
              selections: [{ targets: input.targets }],
            },
        signal,
      }),
    );
  };
  const operation = useRetainedOperation<Intent, void>(
    async (input, key, context) => execute(input, key, context.signal),
    async (input, error, context) => {
      let confirmed = !error;
      if (error && responseOutcomeUnknown(error) && context.operationKey) {
        await execute(input, context.operationKey, context.signal);
        confirmed = true;
      }
      const saved = confirmed
        ? await getManagedBindings(role, model.id, context.signal)
        : undefined;
      const bindingsKey = [...charityKeys.bindings(role, model.id), accountId] as const;
      // Reuse this authoritative read while refreshing dependent lists and counts.
      if (saved) context.commit(() => client.setQueryData(bindingsKey, saved));
      await client.invalidateQueries(
        {
          queryKey: charityKeys.root(role),
          predicate: ({ queryKey }) =>
            !saved ||
            queryKey.length !== bindingsKey.length ||
            bindingsKey.some((part, index) => queryKey[index] !== part),
        },
        { throwOnError: true },
      );
      if (saved) {
        context.commit(() => {
          setExpected(saved.binding_revision);
          if (!binding) setTargets([]);
        });
        if (error) return { operationConfirmed: true };
      }
    },
    charityKeys.root(role),
  );
  const locked =
    operation.isPending ||
    operation.outcome === 'unknown' ||
    operation.outcome === 'refresh-failed';
  const identity = (row: { donation_key_id: string; upstream_model_id: string }) =>
    row.donation_key_id + '\u0000' + row.upstream_model_id;
  const add = (row: CharityBindingCandidate) =>
    setTargets((previous) => [
      ...previous,
      {
        donation_key_id: row.donation_key_id,
        upstream_model_id: row.upstream_model_id,
        native_services: row.capabilities?.can_bind ? [...(row.native_services ?? [])] : [],
        options: row.native_services ?? [],
        label: row.source.canonical_base_url,
      },
    ]);
  return (
    <div className="nb-stack">
      <Note>{t('common.connections.targetOrderNote')}</Note>
      {targets.map((row, index) => (
        <fieldset key={identity(row)} disabled={locked}>
          <legend>
            {index + 1}. {row.label} · {row.upstream_model_id}
          </legend>
          <NativeServicesField
            options={row.options}
            value={row.native_services}
            onChange={(native_services) =>
              setTargets((previous) =>
                previous.map((item, i) => (i === index ? { ...item, native_services } : item)),
              )
            }
          />
          <div className="nb-actions">
            <button
              type="button"
              className="nb-btn nb-btn--secondary"
              disabled={index === 0}
              onClick={() =>
                setTargets((previous) => {
                  const next = previous.slice();
                  [next[index - 1], next[index]] = [next[index]!, next[index - 1]!];
                  return next;
                })
              }
            >
              {t('common.connections.moveUp')}
            </button>
            <button
              type="button"
              className="nb-btn nb-btn--secondary"
              disabled={index === targets.length - 1}
              onClick={() =>
                setTargets((previous) => {
                  const next = previous.slice();
                  [next[index + 1], next[index]] = [next[index]!, next[index + 1]!];
                  return next;
                })
              }
            >
              {t('common.connections.moveDown')}
            </button>
            <button
              type="button"
              className="nb-btn nb-btn--secondary"
              onClick={() => setTargets((previous) => previous.filter((_, i) => i !== index))}
            >
              {t('common.connections.removeTarget')}
            </button>
          </div>
        </fieldset>
      ))}
      <Field label={t('common.connections.modelSearch')}>
        {(props) => (
          <input
            {...props}
            type="search"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
        )}
      </Field>
      {candidates.isPending ? (
        <LoadingState />
      ) : candidates.error ? (
        <ErrorState error={candidates.error} onRetry={() => void candidates.refetch()} />
      ) : candidates.data ? (
        <>
          <DataTable
            caption={t('common.connections.availableTargets')}
            rowKey={identity}
            rows={candidates.data.data}
            columns={[
              {
                key: 'name',
                header: t('common.connections.upstreamModel'),
                cell: 'title',
                render: (row) => (
                  <div>
                    <strong>{row.upstream_model_id}</strong>
                    <p className="nb-sub">
                      {row.source.canonical_base_url} · {row.source.display_head}…
                      {row.source.display_tail}
                    </p>
                  </div>
                ),
              },
              {
                key: 'routes',
                header: t('common.connections.availableServices'),
                cell: 'meta',
                render: (row) =>
                  row.service_routes ? <ServiceRoutesSummary value={row.service_routes} /> : null,
              },
              {
                key: 'add',
                header: t('common.connections.actions'),
                cell: 'action',
                render: (row) => (
                  <button
                    type="button"
                    className="nb-btn nb-btn--secondary"
                    disabled={
                      locked ||
                      !row.capabilities?.can_bind ||
                      targets.some((target) => identity(target) === identity(row))
                    }
                    onClick={() => add(row)}
                  >
                    {t('common.connections.addTarget')}
                  </button>
                ),
              },
            ]}
          />
          <PagePagination
            metadata={candidates.data.pagination}
            requestedPage={pager.page}
            busy={candidates.isFetching || locked}
            onPageChange={pager.setPage}
            onPageSizeChange={pager.setPageSize}
          />
        </>
      ) : null}
      {operation.error ? <ErrorState error={operation.error} /> : null}
      {operation.refreshError ? (
        <ErrorState
          error={operation.refreshError}
          onRetry={() => void operation.refresh().catch(() => undefined)}
        />
      ) : null}
      {operation.outcome === 'unknown' ? (
        <button
          type="button"
          className="nb-btn nb-btn--secondary"
          disabled={operation.isPending}
          onClick={() => void operation.check().catch(() => undefined)}
        >
          {t('common.connections.checkResult')}
        </button>
      ) : null}
      {operation.outcome === 'conflict' ? (
        <Note tone="warn">
          <p>{t('common.connections.conflictDraft')}</p>
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            onClick={() => {
              setExpected(revision);
              operation.reset();
            }}
          >
            {t('common.connections.useCurrentRevision')}
          </button>
        </Note>
      ) : null}
      {operation.outcome === 'confirmed' ? (
        <Note tone="ok">{t('common.connections.saved')}</Note>
      ) : null}
      <button
        type="button"
        className="nb-btn nb-btn--primary"
        disabled={
          locked ||
          operation.outcome === 'conflict' ||
          !targets.length ||
          targets.some((row) => !row.native_services.length)
        }
        onClick={() =>
          void operation
            .mutateAsync({
              expected_binding_revision: expected,
              targets: targets.map(({ donation_key_id, upstream_model_id, native_services }) => ({
                donation_key_id,
                upstream_model_id,
                native_services,
              })),
            })
            .catch(() => undefined)
        }
      >
        {t(binding ? 'common.connections.saveTargets' : 'common.connections.addTargetGroup')}
      </button>
    </div>
  );
}
