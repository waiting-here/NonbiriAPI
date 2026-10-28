import { useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { PageHeader, ErrorState, LoadingState, EmptyState } from '@shared/components/States';
import {
  Button,
  DataTable,
  Field,
  FilterBar,
  Note,
  Panel,
  PanelBody,
  PanelHead,
  Tabs,
} from '@shared/components/ui';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { PagePagination } from '@shared/operations/PagePagination';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { usePagePager } from '@shared/operations/usePagePager';
import { useSearchState } from '@shared/operations/useSearchState';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { responseOutcomeUnknown } from '@shared/operations/api';
import { pageMetadata } from '@shared/agent/api';
import { AgentTime } from '@shared/agent/AgentParts';
import { formatCount } from '@shared/utils/formatNumber';
import {
  getCleanup,
  getHistory,
  getOperation,
  getOperationItems,
  historyRoot,
  prepareCleanup,
  resumeCleanup,
  startCleanup,
  type CleanupJob,
  type Filter,
  type Group,
  type Operation,
  type PreparedCleanup,
  type Subject,
} from './api';
import './management-history.css';

const sources = ['web', 'caller_key', 'mainstream_rule', 'ai_auto', 'ai_manual'];
const outcomes = ['succeeded', 'partial', 'failed', 'rejected', 'no_change', 'accepted', 'unknown'];
const filterFields = [
  'time_from',
  'time_to',
  'source',
  'actor_id',
  'authorizer_id',
  'domain',
  'action',
  'outcome',
  'event_id',
  'target_kind',
  'target_id',
  'group_key',
  'search',
] as const;
const domains = [
  'users',
  'donations',
  'donation-keys',
  'donation-sources',
  'mainstream-channels',
  'charity-models',
  'model-bindings',
  'charity-binding-presets',
  'charity-price-templates',
  'endpoint-tags',
  'endpoint-tag-rules',
  'gateway-model-capabilities',
  'config',
  'blacklist',
  'reports',
  'announcements',
  'maintenance',
  'alerts',
  'abuse-audit',
  'rewards',
  'gacha',
  'games',
  'legal-holds',
  'ai',
  'management-operations',
];
function readFilter(params: URLSearchParams): Filter {
  const filter: Filter = {};
  for (const field of filterFields) {
    const value = params.get('mh_' + field);
    if (!value) continue;
    if (field === 'time_from' || field === 'time_to') {
      const number = Number(value);
      if (Number.isSafeInteger(number) && number > 0 && number <= 253402300799999)
        filter[field] = number;
    } else filter[field] = value;
  }
  return filter;
}
function localDate(value?: number) {
  if (!value) return '';
  const date = new Date(value);
  return new Date(value - date.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
}
function OutcomeBadge({ outcome }: { outcome: string }) {
  const { t } = useTranslation();
  const tone =
    outcome === 'succeeded'
      ? 'ok'
      : ['failed', 'rejected'].includes(outcome)
        ? 'bad'
        : ['partial', 'unknown'].includes(outcome)
          ? 'warn'
          : outcome === 'accepted'
            ? 'info'
            : '';
  return (
    <span className={'nb-badge' + (tone ? ' nb-badge--' + tone : '')}>
      {t(`admin.managementHistory.outcomes.${outcome}`, { defaultValue: outcome })}
    </span>
  );
}
function Executor({
  source,
  actor,
  authorizer,
}: {
  source: string;
  actor: Subject;
  authorizer: Subject;
}) {
  const { t } = useTranslation();
  if (source === 'ai_manual')
    return (
      <span>
        {t('admin.managementHistory.aiAuthorized', {
          name: authorizer.name || authorizer.id || '—',
        })}
      </span>
    );
  if (source === 'ai_auto' || source === 'mainstream_rule')
    return <span>{t(`admin.managementHistory.sources.${source}`)}</span>;
  return <span>{actor.name || actor.id || t('admin.managementHistory.unidentified')}</span>;
}
function CountDetails({
  value,
}: {
  value: Pick<Operation, 'targets' | 'succeeded' | 'failed' | 'skipped'>;
}) {
  const { t } = useTranslation();
  return (
    <span className="management-history__counts">
      {(['succeeded', 'skipped', 'failed'] as const).map((key) => (
        <span key={key}>
          {t(`admin.managementHistory.counts.${key}`)}{' '}
          <strong>{formatCount(value[key]).display}</strong>
        </span>
      ))}
    </span>
  );
}
function ActionTitle({
  domain,
  action,
  summary,
}: {
  domain: string;
  action: string;
  summary?: string;
}) {
  const { t, i18n } = useTranslation();
  const verb = action.split('.').at(-1) ?? action;
  const domainLabel = t(`admin.managementHistory.domains.${domain}`, { defaultValue: domain });
  const verbLabel = t(`admin.managementHistory.actions.${verb}`, { defaultValue: verb });
  const title =
    i18n.resolvedLanguage === 'zh' && summary
      ? summary
      : t('admin.managementHistory.actionTitle', { domain: domainLabel, action: verbLabel });
  return <span title={domain + ' · ' + action}>{title}</span>;
}

function Filters({ filter, onApply }: { filter: Filter; onApply: (value: Filter) => void }) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState<Record<string, string>>(() =>
    Object.fromEntries(
      filterFields.map((key) => [
        key,
        key === 'time_from' || key === 'time_to' ? localDate(filter[key]) : (filter[key] ?? ''),
      ]),
    ),
  );
  const [invalidRange, setInvalidRange] = useState(false);
  const field = (name: string, type = 'text') => (
    <Field label={t(`admin.managementHistory.fields.${name}`)} key={name}>
      {(props) => (
        <input
          {...props}
          type={type}
          value={draft[name] ?? ''}
          onChange={(event) => setDraft((old) => ({ ...old, [name]: event.target.value }))}
        />
      )}
    </Field>
  );
  const select = (name: string, values: string[], labelGroup: string) => (
    <Field label={t(`admin.managementHistory.fields.${name}`)} key={name}>
      {(props) => (
        <select
          {...props}
          value={draft[name] ?? ''}
          onChange={(event) => setDraft((old) => ({ ...old, [name]: event.target.value }))}
        >
          <option value="">{t('admin.managementHistory.all')}</option>
          {values.map((value) => (
            <option key={value} value={value}>
              {t(`admin.managementHistory.${labelGroup}.${value}`, { defaultValue: value })}
            </option>
          ))}
        </select>
      )}
    </Field>
  );
  return (
    <>
      <FilterBar
        ariaLabel={t('admin.managementHistory.fields.search')}
        secondaryLabel={t('admin.managementHistory.moreFilters')}
        activeCount={
          filterFields.filter(
            (key) =>
              draft[key] && !['search', 'source', 'outcome', 'domain', 'group_key'].includes(key),
          ).length
        }
        onSubmit={() => {
          const next: Filter = {};
          for (const key of filterFields) {
            if (key === 'group_key') continue;
            const value = draft[key]?.trim();
            if (!value) continue;
            if (key === 'time_from' || key === 'time_to') next[key] = new Date(value).getTime();
            else next[key] = value;
          }
          if (next.time_from && next.time_to && next.time_from >= next.time_to) {
            setInvalidRange(true);
            return;
          }
          setInvalidRange(false);
          onApply(next);
        }}
        search={
          <div className="management-history__filters">
            <Field
              label={t('admin.managementHistory.fields.search')}
              help={t('admin.managementHistory.searchHelp')}
            >
              {(props) => (
                <input
                  {...props}
                  type="search"
                  placeholder={t('admin.managementHistory.searchHelp')}
                  value={draft.search ?? ''}
                  onChange={(event) => setDraft((old) => ({ ...old, search: event.target.value }))}
                />
              )}
            </Field>
            {select('source', sources, 'sources')}
            {select('outcome', outcomes, 'outcomes')}
            {select('domain', domains, 'domains')}
            <div className="nb-actions">
              <Button type="submit">{t('admin.managementHistory.apply')}</Button>
              <Button type="button" variant="secondary" onClick={() => onApply({})}>
                {t('admin.managementHistory.clearFilters')}
              </Button>
            </div>
          </div>
        }
        secondary={
          <>
            {field('time_from', 'datetime-local')}
            {field('time_to', 'datetime-local')}
            {field('actor_id')}
            {field('authorizer_id')}
            {field('action')}
            {field('target_kind')}
            {field('target_id')}
            {field('event_id')}
            <p className="management-history__filter-help nb-small nb-muted">
              {t('admin.managementHistory.filterHelp')}
            </p>
          </>
        }
      />
      {invalidRange ? <Note tone="warn">{t('admin.managementHistory.invalidRange')}</Note> : null}
    </>
  );
}

export function ManagementHistory({ account }: { account: string }) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const [params, setParams] = useSearchState();
  const filter = readFilter(params);
  const filterKey = JSON.stringify(filter);
  const view = params.get('mh_view') === 'operations' ? 'operations' : 'groups';
  const selected = params.get('mh_detail') ?? '';
  const job = params.get('mh_job') ?? '';
  const root = historyRoot(account);
  const scope = filterKey + ':' + view;
  const snapshot = useRef<{ scope: string; id: string } | null>(null);
  const pager = useUrlPagePager({
    station: 'admin',
    listType: 'management-history',
    scopeKey: account,
    pageParam: 'mh_page',
    pageSizeParam: 'mh_size',
  });
  const query = useQuery({
    queryKey: [...root, 'list', scope, pager.page, pager.pageSize],
    queryFn: ({ signal }) =>
      getHistory(
        filter,
        view,
        pager.page,
        pager.pageSize,
        snapshot.current?.scope === scope ? snapshot.current.id : '',
        signal,
      ),
    retry: false,
  });
  useEffect(() => {
    if (query.data) snapshot.current = { scope, id: query.data.snapshot_max_id };
  }, [query.data, scope]);
  const update = (key: string, value: string) =>
    setParams((old) => {
      const next = new URLSearchParams(old);
      if (value) next.set(key, value);
      else next.delete(key);
      return next;
    });
  const apply = (value: Filter) =>
    setParams((old) => {
      const next = new URLSearchParams(old);
      for (const key of filterFields) {
        next.delete('mh_' + key);
        if (value[key] !== undefined) next.set('mh_' + key, String(value[key]));
      }
      next.delete('mh_page');
      next.delete('mh_detail');
      next.delete('mh_group_page');
      return next;
    });
  const changeView = (value: string) =>
    setParams((old) => {
      const next = new URLSearchParams(old);
      next.set('mh_view', value);
      next.delete('mh_detail');
      if (value === 'groups' && old.has('mh_group_key')) {
        next.delete('mh_group_key');
        next.set('mh_page', old.get('mh_group_page') ?? '1');
        next.delete('mh_group_page');
      } else {
        next.delete('mh_page');
      }
      return next;
    });
  const refresh = async () => {
    snapshot.current = null;
    await client.invalidateQueries({ queryKey: [...root, 'list'] });
  };
  const columns = [
    {
      key: 'action',
      header: t('admin.managementHistory.action'),
      cell: 'title' as const,
      render: (row: Group) => <ActionTitle domain={row.domain} action={row.action} />,
    },
    {
      key: 'time',
      header: t('admin.managementHistory.hour'),
      cell: 'meta' as const,
      render: (row: Group) => <AgentTime value={row.bucket} />,
    },
    {
      key: 'actor',
      header: t('admin.managementHistory.executor'),
      cell: 'meta' as const,
      render: (row: Group) => <Executor {...row} />,
    },
    {
      key: 'source',
      header: t('admin.managementHistory.fields.source'),
      cell: 'meta' as const,
      render: (row: Group) => (
        <span>
          {t(`admin.managementHistory.sources.${row.source}`, { defaultValue: row.source })}
        </span>
      ),
    },
    {
      key: 'outcome',
      header: t('admin.managementHistory.fields.outcome'),
      cell: 'status' as const,
      render: (row: Group) => <OutcomeBadge outcome={row.outcome} />,
    },
    {
      key: 'counts',
      header: t('admin.managementHistory.targetResults'),
      cell: 'meta' as const,
      render: (row: Group) => <CountDetails value={row} />,
    },
    {
      key: 'open',
      header: t('admin.managementHistory.operations'),
      cell: 'action' as const,
      render: (row: Group) => (
        <Button
          variant="secondary"
          size="sm"
          onClick={() => {
            setParams((old) => {
              const next = new URLSearchParams(old);
              next.set('mh_group_key', row.key);
              next.set('mh_group_page', pager.page);
              next.set('mh_view', 'operations');
              next.delete('mh_page');
              next.delete('mh_detail');
              return next;
            });
          }}
        >
          {t('admin.managementHistory.viewCount', { value: formatCount(row.operations).display })}
        </Button>
      ),
    },
  ];
  return (
    <div className="page ops-stack management-history">
      <PageHeader
        eyebrow={t('admin.navigation.operations')}
        title={t('admin.managementHistory.title')}
        description={t('admin.managementHistory.description')}
      />
      <Panel>
        <PanelBody>
          <Filters key={filterKey} filter={filter} onApply={apply} />
        </PanelBody>
      </Panel>
      <Panel>
        <PanelHead
          title={t('admin.managementHistory.records')}
          description={t('admin.managementHistory.groupHelp')}
        />
        <PanelBody>
          <div className="nb-actions">
            <Tabs
              label={t('admin.managementHistory.view')}
              value={query.data?.view ?? view}
              onChange={changeView}
              tabs={[
                { value: 'groups', label: t('admin.managementHistory.groups') },
                { value: 'operations', label: t('admin.managementHistory.individual') },
              ]}
            />
            <Button variant="secondary" onClick={() => void refresh()} disabled={query.isFetching}>
              {t('common.refresh')}
            </Button>
          </div>
          {filter.group_key ? (
            <Note>
              {t('admin.managementHistory.selectedGroup')}{' '}
              <Button variant="ghost" size="sm" onClick={() => changeView('groups')}>
                {t('admin.managementHistory.backToGroups')}
              </Button>
            </Note>
          ) : null}
          {query.isPending ? (
            <LoadingState />
          ) : query.error ? (
            <ErrorState error={query.error} onRetry={() => void query.refetch()} />
          ) : (
            <>
              {query.data.total === '0' ? (
                <EmptyState
                  title={t('admin.managementHistory.empty')}
                  body={t('admin.managementHistory.emptyHelp')}
                />
              ) : query.data.view === 'groups' ? (
                <DataTable
                  caption={t('admin.managementHistory.groups')}
                  rows={query.data.groups}
                  rowKey={(row) => row.key}
                  columns={columns}
                />
              ) : (
                <DataTable
                  caption={t('admin.managementHistory.individual')}
                  rows={query.data.operations}
                  rowKey={(row) => row.id}
                  selectedKey={selected}
                  columns={[
                    {
                      key: 'action',
                      header: t('admin.managementHistory.action'),
                      cell: 'title',
                      render: (row) => <ActionTitle {...row} />,
                    },
                    {
                      key: 'time',
                      header: t('admin.managementHistory.time'),
                      cell: 'meta',
                      render: (row) => <AgentTime value={row.created_at} />,
                    },
                    {
                      key: 'actor',
                      header: t('admin.managementHistory.executor'),
                      cell: 'meta',
                      render: (row) => <Executor {...row} />,
                    },
                    {
                      key: 'outcome',
                      header: t('admin.managementHistory.fields.outcome'),
                      cell: 'status',
                      render: (row) => <OutcomeBadge outcome={row.outcome} />,
                    },
                    {
                      key: 'counts',
                      header: t('admin.managementHistory.targetResults'),
                      cell: 'meta',
                      render: (row) => <CountDetails value={row} />,
                    },
                    {
                      key: 'detail',
                      header: t('admin.managementHistory.details'),
                      cell: 'action',
                      render: (row) => (
                        <Button
                          variant="secondary"
                          size="sm"
                          onClick={() => update('mh_detail', row.id)}
                        >
                          {t('admin.managementHistory.details')} · {row.id}
                        </Button>
                      ),
                    },
                  ]}
                />
              )}
              <PagePagination
                metadata={pageMetadata(query.data)}
                requestedPage={pager.page}
                busy={query.isFetching}
                onPageChange={pager.setPage}
                onPageSizeChange={pager.setPageSize}
              />
            </>
          )}
        </PanelBody>
      </Panel>
      {selected ? (
        <OperationDetail
          key={selected}
          id={selected}
          account={account}
          onClose={() => update('mh_detail', '')}
          onJob={(value) => update('mh_job', value)}
        />
      ) : null}
      <CleanupPanel account={account} filter={filter} onJob={(value) => update('mh_job', value)} />
      {job ? (
        <CleanupProgress
          key={job}
          id={job}
          account={account}
          onClose={() => update('mh_job', '')}
        />
      ) : null}
    </div>
  );
}

function OperationDetail({
  id,
  account,
  onClose,
  onJob,
}: {
  id: string;
  account: string;
  onClose: () => void;
  onJob: (value: string) => void;
}) {
  const { t } = useTranslation();
  const pager = usePagePager({
    station: 'admin',
    listType: 'management-history-targets',
    scopeKey: account + ':' + id,
  });
  const detail = useQuery({
    queryKey: [...historyRoot(account), 'detail', id],
    queryFn: ({ signal }) => getOperation(id, signal),
    retry: false,
  });
  const items = useQuery({
    queryKey: [...historyRoot(account), 'items', id, pager.page, pager.pageSize],
    queryFn: ({ signal }) => getOperationItems(id, pager.page, pager.pageSize, signal),
    retry: false,
  });
  return (
    <Panel>
      <PanelHead title={t('admin.managementHistory.detailTitle', { id })} />
      <PanelBody>
        <div className="nb-actions">
          <Button variant="secondary" onClick={onClose}>
            {t('admin.managementHistory.closeDetails')}
          </Button>
        </div>
        {detail.isPending ? (
          <LoadingState />
        ) : detail.error ? (
          <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />
        ) : (
          <div className="nb-stack">
            <p>
              <ActionTitle {...detail.data} /> · <OutcomeBadge outcome={detail.data.outcome} />
            </p>
            <p>
              <Executor {...detail.data} /> · <AgentTime value={detail.data.created_at} />
            </p>
            <dl className="management-history__facts">
              <dt>{t('admin.managementHistory.fields.source')}</dt>
              <dd>
                {t(`admin.managementHistory.sources.${detail.data.source}`, {
                  defaultValue: detail.data.source,
                })}
              </dd>
              <dt>{t('admin.managementHistory.fields.event_id')}</dt>
              <dd>{detail.data.event_id}</dd>
              <dt>{t('admin.managementHistory.fields.domain')}</dt>
              <dd>{detail.data.domain}</dd>
              <dt>{t('admin.managementHistory.fields.action')}</dt>
              <dd>{detail.data.action}</dd>
              {detail.data.authorization_ref ? (
                <>
                  <dt>{t('admin.managementHistory.authorization')}</dt>
                  <dd>{detail.data.authorization_ref}</dd>
                </>
              ) : null}
              {detail.data.correlation_id ? (
                <>
                  <dt>{t('admin.managementHistory.related')}</dt>
                  <dd>
                    {detail.data.correlation_kind} · {detail.data.correlation_id}{' '}
                    {detail.data.correlation_kind === 'cleanup_job' ? (
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => onJob(detail.data.correlation_id!)}
                      >
                        {t('admin.managementHistory.cleanupProgress')}
                      </Button>
                    ) : null}
                  </dd>
                </>
              ) : null}
              {detail.data.code ? (
                <>
                  <dt>{t('admin.managementHistory.resultCode')}</dt>
                  <dd>{detail.data.code}</dd>
                </>
              ) : null}
            </dl>
            <CountDetails value={detail.data} />
          </div>
        )}
        {items.isPending ? (
          <LoadingState />
        ) : items.error ? (
          <ErrorState error={items.error} onRetry={() => void items.refetch()} />
        ) : (
          <>
            {items.data.items.length ? (
              <DataTable
                caption={t('admin.managementHistory.targets')}
                rows={items.data.items}
                rowKey={(item) => item.ordinal}
                columns={[
                  {
                    key: 'target',
                    header: t('admin.managementHistory.target'),
                    cell: 'title',
                    render: (item) => (
                      <span>
                        {item.target_name || item.target_id || '—'}
                        <small className="nb-muted management-history__target-kind">
                          {item.target_kind}{' '}
                          {item.target_name && item.target_id ? ' · ' + item.target_id : ''}
                        </small>
                      </span>
                    ),
                  },
                  {
                    key: 'outcome',
                    header: t('admin.managementHistory.fields.outcome'),
                    cell: 'status',
                    render: (item) => <OutcomeBadge outcome={item.outcome} />,
                  },
                  {
                    key: 'code',
                    header: t('admin.managementHistory.resultCode'),
                    cell: 'meta',
                    render: (item) => <span>{item.code || '—'}</span>,
                  },
                  {
                    key: 'changes',
                    header: t('admin.managementHistory.changedFields'),
                    cell: 'meta',
                    render: (item) => (
                      <span>{item.changes.map((change) => change.field).join(', ') || '—'}</span>
                    ),
                  },
                ]}
              />
            ) : (
              <p className="nb-muted">{t('admin.managementHistory.noTargets')}</p>
            )}
            <PagePagination
              metadata={pageMetadata(items.data)}
              requestedPage={pager.page}
              busy={items.isFetching}
              onPageChange={pager.setPage}
              onPageSizeChange={pager.setPageSize}
            />
          </>
        )}
      </PanelBody>
    </Panel>
  );
}

function CleanupPanel({
  account,
  filter,
  onJob,
}: {
  account: string;
  filter: Filter;
  onJob: (id: string) => void;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const [preview, setPreview] = useState<{ prepared: PreparedCleanup; total: string } | null>(null);
  const prepare = useRetainedOperation<Filter, PreparedCleanup>(
    async (input, _key, context) => {
      const prepared = await prepareCleanup(input, context.signal);
      const page =
        prepared.cutoff_max_id === '0'
          ? null
          : await getHistory(input, 'operations', '1', 10, prepared.cutoff_max_id, context.signal);
      context.commit(() => setPreview({ prepared, total: page?.total ?? '0' }));
      return prepared;
    },
    () => undefined,
    ['admin'],
  );
  const cleanup = useRetainedOperation<PreparedCleanup, CleanupJob>(
    async (input, key, context) => {
      const result = await startCleanup(input, key, context.signal);
      context.commit(() => {
        onJob(result.id);
        setPreview(null);
      });
      return result;
    },
    async (_input, error) => {
      if (!error) await client.invalidateQueries({ queryKey: historyRoot(account) });
    },
    ['admin'],
  );
  return (
    <Panel>
      <PanelHead
        title={t('admin.managementHistory.cleanupTitle')}
        description={t('admin.managementHistory.cleanupHelp')}
      />
      <PanelBody>
        <Button
          variant="secondary"
          disabled={prepare.isPending || cleanup.isPending || cleanup.outcome === 'unknown'}
          onClick={() => {
            cleanup.reset();
            prepare.mutate(filter);
          }}
        >
          {Object.keys(filter).length
            ? t('admin.managementHistory.cleanupFiltered')
            : t('admin.managementHistory.cleanupAll')}
        </Button>
        {prepare.error ? <ErrorState error={prepare.error} /> : null}
        {cleanup.error && !preview ? <ErrorState error={cleanup.error} /> : null}
        {cleanup.outcome === 'unknown' && !preview ? (
          <Note tone="warn">
            <p>{t('admin.managementHistory.unknownCleanup')}</p>
            <Button
              variant="secondary"
              disabled={cleanup.isPending}
              onClick={() => cleanup.variables && cleanup.mutate(cleanup.variables)}
            >
              {t('common.connections.checkResult')}
            </Button>
          </Note>
        ) : null}
        <ConfirmDialog
          open={Boolean(preview)}
          title={t('admin.managementHistory.cleanupConfirm')}
          description={
            preview
              ? t('admin.managementHistory.cleanupScope', {
                  value: formatCount(preview.total).display,
                  cutoff: preview.prepared.cutoff_max_id,
                })
              : ''
          }
          confirmLabel={t('admin.managementHistory.cleanupStart')}
          danger
          busy={cleanup.isPending}
          confirmDisabled={preview?.total === '0' || cleanup.outcome === 'unknown'}
          onConfirm={() => preview && cleanup.mutate(preview.prepared)}
          onCancel={() => setPreview(null)}
        >
          <p>{t('admin.managementHistory.cleanupConsequence')}</p>
          {preview ? (
            <dl className="management-history__facts">
              <dt>{t('admin.managementHistory.scope')}</dt>
              <dd className="nb-stack">
                {preview.prepared.all
                  ? t('admin.managementHistory.allRecords')
                  : Object.entries(preview.prepared.filter).map(([key, value]) => (
                      <span key={key}>
                        {t(`admin.managementHistory.fields.${key}`, { defaultValue: key })}:{' '}
                        {typeof value === 'number' ? (
                          <AgentTime value={value} />
                        ) : key === 'source' ? (
                          t(`admin.managementHistory.sources.${value}`, { defaultValue: value })
                        ) : key === 'outcome' ? (
                          t(`admin.managementHistory.outcomes.${value}`, { defaultValue: value })
                        ) : key === 'domain' ? (
                          t(`admin.managementHistory.domains.${value}`, { defaultValue: value })
                        ) : (
                          value
                        )}
                      </span>
                    ))}
              </dd>
            </dl>
          ) : null}
          {cleanup.error ? <ErrorState error={cleanup.error} /> : null}
          {cleanup.outcome === 'unknown' ? (
            <Note tone="warn">
              <p>{t('admin.managementHistory.unknownCleanup')}</p>
              <Button
                variant="secondary"
                disabled={cleanup.isPending}
                onClick={() => cleanup.variables && cleanup.mutate(cleanup.variables)}
              >
                {t('common.connections.checkResult')}
              </Button>
            </Note>
          ) : null}
        </ConfirmDialog>
      </PanelBody>
    </Panel>
  );
}

function CleanupProgress({
  id,
  account,
  onClose,
}: {
  id: string;
  account: string;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const root = historyRoot(account);
  const query = useQuery({
    queryKey: [...root, 'cleanup', id],
    queryFn: ({ signal }) => getCleanup(id, signal),
    retry: false,
    refetchInterval: (query) =>
      ['pending', 'running'].includes(query.state.data?.state ?? '') ? 2000 : false,
  });
  useEffect(() => {
    if (query.data?.state !== 'completed') return;
    const key = historyRoot(account);
    void client.invalidateQueries({ queryKey: [...key, 'list'] });
    void client.invalidateQueries({ queryKey: [...key, 'detail'] });
    void client.invalidateQueries({ queryKey: [...key, 'items'] });
  }, [account, client, query.data?.state]);
  const resume = useRetainedOperation<string, void>(
    async (value, _key, context) => resumeCleanup(value, context.signal),
    async (value, error, context) => {
      const current = await getCleanup(value, context.signal);
      context.commit(() => client.setQueryData([...root, 'cleanup', value], current));
      // A successful status read resolves uncertainty, including a job still needing a retry.
      if (error && responseOutcomeUnknown(error)) return { operationConfirmed: true };
    },
    ['admin'],
  );
  return (
    <Panel>
      <PanelHead title={t('admin.managementHistory.progressTitle', { id })} />
      <PanelBody>
        <div className="nb-actions">
          <Button variant="secondary" onClick={onClose}>
            {t('admin.managementHistory.hideProgress')}
          </Button>
        </div>
        {query.isPending ? (
          <LoadingState />
        ) : query.error ? (
          <ErrorState error={query.error} onRetry={() => void query.refetch()} />
        ) : (
          <div className="nb-stack">
            <p>
              {t(`admin.managementHistory.cleanupStates.${query.data.state}`, {
                defaultValue: query.data.state,
              })}{' '}
              · <AgentTime value={query.data.updated_at} />
            </p>
            <div className="management-history__counts">
              <span>
                {t('admin.managementHistory.deletedOperations', {
                  value: formatCount(query.data.deleted_operations).display,
                })}
              </span>
              <span>
                {t('admin.managementHistory.deletedItems', {
                  value: formatCount(query.data.deleted_items).display,
                })}
              </span>
              <span>
                {t('admin.managementHistory.skippedActive', {
                  value: formatCount(query.data.skipped_active).display,
                })}
              </span>
            </div>
            {query.data.state === 'failed' ? (
              <Note tone="warn">
                <p>
                  {t('admin.managementHistory.cleanupFailed')} {query.data.error_code}
                </p>
                <Button
                  variant="secondary"
                  disabled={resume.isPending}
                  onClick={() => {
                    if (resume.outcome === 'unknown') void resume.check();
                    else if (resume.outcome === 'refresh-failed') void resume.refresh();
                    else resume.mutate(id);
                  }}
                >
                  {t(
                    resume.outcome === 'unknown' || resume.outcome === 'refresh-failed'
                      ? 'common.connections.checkResult'
                      : 'admin.managementHistory.resume',
                  )}
                </Button>
              </Note>
            ) : null}
            {resume.refreshError || resume.error ? (
              <ErrorState error={resume.refreshError ?? resume.error} />
            ) : null}
            <p className="nb-small nb-muted">{t('admin.managementHistory.progressHelp')}</p>
          </div>
        )}
      </PanelBody>
    </Panel>
  );
}
