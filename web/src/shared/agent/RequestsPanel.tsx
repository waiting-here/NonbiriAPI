import { sourceLabels } from './presentation';
import { useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { LoadingState } from '@shared/components/States';
import {
  Button,
  DataTable,
  Field,
  FilterBar,
  Fold,
  Note,
  Panel,
  PanelBody,
  PanelHead,
  Tabs,
} from '@shared/components/ui';
import { useSearchState } from '@shared/operations/useSearchState';
import { PagePagination } from '@shared/operations/PagePagination';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { usePagePager } from '@shared/operations/usePagePager';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { responseOutcomeUnknown } from '@shared/operations/api';
import { formatCount } from '@shared/utils/formatNumber';
import {
  agentRoot,
  cleanupRequests,
  getRequest,
  getRequests,
  getUsage,
  pageMetadata,
  type CleanupInput,
  type CleanupResult,
  type RequestFilters,
  type TokenUsage,
} from './api';
import { AgentEmpty, AgentError, AgentState, AgentTime, RawData } from './AgentParts';

const tokenLabels: Record<keyof Omit<TokenUsage, 'unknown'>, string> = {
  input_tokens: 'common.agent.tokens.input',
  cached_input_tokens: 'common.agent.tokens.cached',
  cache_write_input_tokens: 'common.agent.tokens.cacheWrite',
  output_tokens: 'common.agent.tokens.output',
  reasoning_tokens: 'common.agent.tokens.reasoning',
};
export function UsageTokens({ usage }: { usage: TokenUsage }) {
  const { t } = useTranslation();
  return (
    <div className="agent-token-grid">
      {(Object.keys(tokenLabels) as (keyof typeof tokenLabels)[]).map((key) => (
        <div key={key}>
          <span>{t(tokenLabels[key])}</span>
          <strong className="agent-number">
            {usage.unknown && usage[key] === '0'
              ? t('common.agent.unknownUsage')
              : formatCount(usage[key]).display}
          </strong>
        </div>
      ))}
      {usage.unknown ? <p>{t('common.agent.usageIncomplete')}</p> : null}
    </div>
  );
}
function UsagePanel({ account, active }: { account: string; active: boolean }) {
  const { t } = useTranslation();
  const [window, setWindow] = useState<'total' | 'last_24_hours'>('total');
  const query = useQuery({
    queryKey: [...agentRoot('admin', account), 'usage'],
    queryFn: ({ signal }) => getUsage(signal),
    enabled: active,
    retry: false,
  });
  const pager = usePagePager({
    station: 'admin',
    scopeKey: account,
    listType: 'ai-usage',
    resetKey: window,
  });
  const rows = query.data?.[window] ?? [];
  const pages = Math.max(1, Math.ceil(rows.length / pager.pageSize));
  const current = Math.min(Number(pager.page), pages);
  return (
    <div className="nb-stack">
      <Tabs
        label={t('common.agent.usage')}
        value={window}
        onChange={setWindow}
        tabs={[
          { value: 'total', label: t('common.agent.allTime') },
          { value: 'last_24_hours', label: t('common.agent.last24Hours') },
        ]}
      />
      {query.isPending ? (
        <LoadingState />
      ) : query.error ? (
        <AgentError error={query.error} onRetry={() => void query.refetch()} />
      ) : (
        <>
          {rows.length ? (
            <DataTable
              caption={t('common.agent.usage')}
              rows={rows.slice((current - 1) * pager.pageSize, current * pager.pageSize)}
              rowKey={(row) => [row.actor_id, row.source, row.kind].join(':')}
              columns={[
                {
                  key: 'actor',
                  header: t('common.agent.actor'),
                  cell: 'title',
                  render: (row) => <span>{row.actor_label || row.actor_id}</span>,
                },
                {
                  key: 'source',
                  header: t('common.agent.source'),
                  cell: 'meta',
                  render: (row) => (
                    <span>
                      {t(sourceLabels[row.source] ?? 'common.agent.sources.auto_review')} ·{' '}
                      {t(
                        row.kind === 'compaction'
                          ? 'common.agent.compaction'
                          : 'common.agent.modelRequest',
                      )}
                    </span>
                  ),
                },
                {
                  key: 'count',
                  header: t('common.agent.requests'),
                  cell: 'meta',
                  align: 'num',
                  render: (row) => formatCount(row.requests).display,
                },
                {
                  key: 'tokens',
                  header: t('common.agent.usage'),
                  cell: 'meta',
                  render: (row) => <UsageTokens usage={row.usage} />,
                },
              ]}
            />
          ) : (
            <AgentEmpty body={t('common.agent.usageEmpty')} />
          )}
          <PagePagination
            metadata={{
              page: String(current),
              page_size: pager.pageSize,
              total_items: String(rows.length),
              total_pages: String(pages),
            }}
            requestedPage={pager.page}
            busy={query.isFetching}
            onPageChange={pager.setPage}
            onPageSizeChange={pager.setPageSize}
          />
        </>
      )}
    </div>
  );
}
export function RequestsPanel({ account, active }: { account: string; active: boolean }) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const [params, setParams] = useSearchState();
  const filters: RequestFilters = {
    source: params.get('ai_source') ?? '',
    kind: params.get('ai_kind') ?? '',
    state: params.get('ai_state') ?? '',
    actor_id: params.get('ai_actor') ?? '',
  };
  const [filterDraft, setFilterDraft] = useState(filters);
  const [selected, setSelected] = useState(new Set<string>());
  const [cleanup, setCleanup] = useState<{ body: CleanupInput; count: string } | null>(null);
  const [usageOpen, setUsageOpen] = useState(false);
  const root = agentRoot('admin', account);
  const selectedId = params.get('ai_request') ?? '';
  const pager = useUrlPagePager({
    station: 'admin',
    listType: 'ai-requests',
    scopeKey: account,
    resetKey: JSON.stringify(filters),
    pageParam: 'ai_requests_page',
    pageSizeParam: 'ai_requests_page_size',
  });
  const query = useQuery({
    queryKey: [...root, 'requests', filters, pager.page, pager.pageSize],
    queryFn: ({ signal }) => getRequests(filters, pager.page, pager.pageSize, signal),
    enabled: active,
    retry: false,
  });
  const detail = useQuery({
    queryKey: [...root, 'request', selectedId],
    queryFn: ({ signal }) => getRequest(selectedId, signal),
    enabled: active && Boolean(selectedId),
    retry: false,
  });
  const select = (id: string) =>
    setParams((previous) => {
      const next = new URLSearchParams(previous);
      if (id) next.set('ai_request', id);
      else next.delete('ai_request');
      return next;
    });
  const refresh = () =>
    client.invalidateQueries({ queryKey: [...root, 'requests'] }, { throwOnError: true });
  const operation = useRetainedOperation<CleanupInput, CleanupResult>(
    (input, _key, context) => cleanupRequests(input, context.signal),
    async (input, error, context) => {
      if (error && responseOutcomeUnknown(error)) await cleanupRequests(input, context.signal);
      if (!error || responseOutcomeUnknown(error))
        context.commit(() => {
          setCleanup(null);
          setSelected(new Set());
        });
      await refresh();
      if (error && responseOutcomeUnknown(error)) return { operationConfirmed: true };
    },
    ['admin'],
  );
  const locked =
    operation.isPending || operation.outcome === 'unknown' || Boolean(operation.refreshError);
  const commitFilters = () => {
    setSelected(new Set());
    pager.reset();
    setParams((previous) => {
      const next = new URLSearchParams(previous);
      for (const [key, value] of Object.entries(filterDraft)) {
        const param = key === 'actor_id' ? 'ai_actor' : 'ai_' + key;
        if (value) next.set(param, value);
        else next.delete(param);
      }
      return next;
    });
  };
  const beginCleanup = (selection: boolean) => {
    operation.reset();
    const filter = { ...filters, actor_id: filters.actor_id || undefined };
    setCleanup({
      body: selection
        ? { ...filter, ids: [...selected] }
        : { ...filter, through_id: query.data!.cutoff_id! },
      count: selection ? String(selected.size) : query.data!.total,
    });
  };
  const feedback = (
    <>
      {operation.error ? <AgentError error={operation.error} /> : null}
      {operation.outcome === 'unknown' ? (
        <Note tone="warn">
          <p>{t('common.agent.unknownWrite')}</p>
          <Button
            variant="secondary"
            disabled={operation.isPending}
            onClick={() => void operation.check()}
          >
            {t('common.connections.checkResult')}
          </Button>
        </Note>
      ) : null}
      {operation.refreshError ? (
        <AgentError error={operation.refreshError} onRetry={() => void operation.refresh()} />
      ) : null}
    </>
  );
  return (
    <Panel>
      <PanelHead
        title={t('common.agent.requestHistory')}
        description={t('common.agent.retentionHelp')}
      />
      <PanelBody>
        <Fold title={t('common.agent.usage')} open={usageOpen} onOpenChange={setUsageOpen} lazy>
          <UsagePanel account={account} active={active && usageOpen} />
        </Fold>
        {selectedId ? (
          <div className="nb-stack">
            <Button variant="secondary" onClick={() => select('')}>
              {t('common.agent.backList')}
            </Button>
            {detail.isPending ? (
              <LoadingState />
            ) : detail.error ? (
              <AgentError error={detail.error} onRetry={() => void detail.refetch()} />
            ) : (
              <>
                <div className="nb-inline">
                  <AgentState state={detail.data.state} />
                  <span>{detail.data.actor_label || detail.data.actor_id}</span>
                  <AgentTime value={detail.data.created_at} />
                </div>
                {!detail.data.response_complete ? (
                  <Note tone="warn">{t('common.agent.responseIncomplete')}</Note>
                ) : null}
                <UsageTokens usage={detail.data.usage} />
                <RawData
                  title={t('common.agent.submittedInput')}
                  value={detail.data.queued_input}
                />
                <RawData title={t('common.agent.actualPrompt')} value={detail.data.request_body} />
                <RawData title={t('common.agent.fullResponse')} value={detail.data.response_body} />
                {detail.data.response_body_base64 ? (
                  <RawData
                    title={t('common.agent.binaryResponse')}
                    value={detail.data.response_body_base64}
                  />
                ) : null}
                {detail.data.error_code ? (
                  <RawData
                    title={t('common.agent.failureDetails')}
                    value={detail.data.error_code}
                  />
                ) : null}
              </>
            )}
          </div>
        ) : (
          <>
            <FilterBar
              ariaLabel={t('common.agent.requestFilters')}
              activeCount={Object.values(filters).filter(Boolean).length}
              secondaryLabel={t('common.agent.moreFilters')}
              onSubmit={commitFilters}
              onClearAll={() => {
                setFilterDraft({ source: '', kind: '', state: '', actor_id: '' });
                setSelected(new Set());
                setParams((previous) => {
                  const next = new URLSearchParams(previous);
                  for (const key of ['ai_source', 'ai_kind', 'ai_state', 'ai_actor'])
                    next.delete(key);
                  return next;
                });
              }}
              clearAllLabel={t('common.connections.all')}
              search={
                <Field label={t('common.agent.actor')}>
                  {(props) => (
                    <input
                      {...props}
                      inputMode="numeric"
                      placeholder={t('common.agent.actorPlaceholder')}
                      value={filterDraft.actor_id}
                      onChange={(event) =>
                        setFilterDraft((old) => ({ ...old, actor_id: event.target.value }))
                      }
                    />
                  )}
                </Field>
              }
              secondary={
                <div className="agent-field-grid">
                  <Field label={t('common.agent.source')}>
                    {(props) => (
                      <select
                        {...props}
                        value={filterDraft.source}
                        onChange={(event) =>
                          setFilterDraft((old) => ({ ...old, source: event.target.value }))
                        }
                      >
                        <option value="">{t('common.connections.all')}</option>
                        {Object.entries(sourceLabels).map(([value, label]) => (
                          <option key={value} value={value}>
                            {t(label)}
                          </option>
                        ))}
                      </select>
                    )}
                  </Field>
                  <Field label={t('common.agent.status')}>
                    {(props) => (
                      <select
                        {...props}
                        value={filterDraft.state}
                        onChange={(event) =>
                          setFilterDraft((old) => ({ ...old, state: event.target.value }))
                        }
                      >
                        <option value="">{t('common.connections.all')}</option>
                        {[
                          'pending',
                          'running',
                          'succeeded',
                          'failed',
                          'cancelled',
                          'interrupted',
                        ].map((value) => (
                          <option key={value} value={value}>
                            {t(
                              (
                                {
                                  pending: 'common.agent.states.pending',
                                  running: 'common.agent.states.running',
                                  succeeded: 'common.agent.states.succeeded',
                                  failed: 'common.agent.states.failed',
                                  cancelled: 'common.agent.states.cancelled',
                                  interrupted: 'common.agent.states.interrupted',
                                } as Record<string, string>
                              )[value]!,
                            )}
                          </option>
                        ))}
                      </select>
                    )}
                  </Field>
                  <Field label={t('common.agent.requestKind')}>
                    {(props) => (
                      <select
                        {...props}
                        value={filterDraft.kind}
                        onChange={(event) =>
                          setFilterDraft((old) => ({ ...old, kind: event.target.value }))
                        }
                      >
                        <option value="">{t('common.connections.all')}</option>
                        <option value="model">{t('common.agent.modelRequest')}</option>
                        <option value="compaction">{t('common.agent.compaction')}</option>
                      </select>
                    )}
                  </Field>
                </div>
              }
            />
            {query.isPending ? (
              <LoadingState />
            ) : query.error ? (
              <AgentError error={query.error} onRetry={() => void query.refetch()} />
            ) : (
              <>
                {query.data.items.length ? (
                  <DataTable
                    caption={t('common.agent.requestHistory')}
                    rows={query.data.items}
                    rowKey={(row) => row.id}
                    columns={[
                      {
                        key: 'selection',
                        header: t('common.agent.select'),
                        cell: 'meta',
                        render: (row) => (
                          <Field label={t('common.agent.selectRequest', { id: row.id })}>
                            {(props) => (
                              <input
                                {...props}
                                type="checkbox"
                                checked={selected.has(row.id)}
                                disabled={locked}
                                onChange={(event) =>
                                  setSelected((previous) => {
                                    const next = new Set(previous);
                                    if (event.target.checked) next.add(row.id);
                                    else next.delete(row.id);
                                    return next;
                                  })
                                }
                              />
                            )}
                          </Field>
                        ),
                      },
                      {
                        key: 'actor',
                        header: t('common.agent.actor'),
                        cell: 'title',
                        render: (row) => (
                          <div className="nb-stack nb-stack--sm">
                            <strong>{row.actor_label || row.actor_id}</strong>
                            <span>
                              {t(sourceLabels[row.source] ?? 'common.agent.sources.auto_review')}
                            </span>
                          </div>
                        ),
                      },
                      {
                        key: 'state',
                        header: t('common.agent.status'),
                        cell: 'status',
                        render: (row) => <AgentState state={row.state} />,
                      },
                      {
                        key: 'time',
                        header: t('common.agent.created'),
                        cell: 'meta',
                        render: (row) => <AgentTime value={row.created_at} />,
                      },
                      {
                        key: 'actions',
                        header: t('common.agent.actions'),
                        cell: 'action',
                        render: (row) => (
                          <Button variant="secondary" onClick={() => select(row.id)}>
                            {t('common.agent.open')}
                          </Button>
                        ),
                      },
                    ]}
                  />
                ) : (
                  <AgentEmpty body={t('common.agent.requestsEmpty')} />
                )}
                <PagePagination
                  metadata={pageMetadata(query.data)}
                  requestedPage={pager.page}
                  busy={query.isFetching}
                  onPageChange={pager.setPage}
                  onPageSizeChange={pager.setPageSize}
                />
                <div className="nb-actions">
                  <Button
                    variant="danger-outline"
                    disabled={locked || !selected.size}
                    onClick={() => beginCleanup(true)}
                  >
                    {t('common.agent.cleanupSelected', {
                      count: formatCount(selected.size).display,
                    })}
                  </Button>
                  <Button
                    variant="danger-outline"
                    disabled={locked || !query.data.cutoff_id || query.data.cutoff_id === '0'}
                    onClick={() => beginCleanup(false)}
                  >
                    {t('common.agent.cleanupMatching')}
                  </Button>
                </div>
              </>
            )}
            {operation.isSuccess ? (
              <Note tone="ok">
                {operation.data
                  ? t('common.agent.cleaned', {
                      deleted: operation.data?.deleted_count ?? '0',
                      skipped: operation.data?.skipped_count ?? '0',
                    })
                  : t('common.outcome.saved')}
              </Note>
            ) : null}
            {!cleanup ? feedback : null}
          </>
        )}
      </PanelBody>
      <ConfirmDialog
        open={Boolean(cleanup)}
        title={t('common.agent.cleanupTitle')}
        description={t('common.agent.cleanupHelp', {
          count: formatCount(cleanup?.count ?? '0').display,
        })}
        confirmLabel={t('common.agent.cleanupConfirm')}
        danger
        busy={operation.isPending}
        confirmDisabled={operation.outcome === 'unknown'}
        onCancel={() => setCleanup(null)}
        onConfirm={() => cleanup && operation.mutate(cleanup.body)}
      >
        {feedback}
      </ConfirmDialog>
    </Panel>
  );
}
