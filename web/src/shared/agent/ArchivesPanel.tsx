import { roleLabels } from './presentation';
import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { useSearchState } from '@shared/operations/useSearchState';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { PagePagination } from '@shared/operations/PagePagination';
import { LoadingState } from '@shared/components/States';
import { Button, DataTable, Panel, PanelBody, PanelHead, Tabs } from '@shared/components/ui';
import {
  agentRoot,
  getArchives,
  getArchiveMessages,
  getArchivePlans,
  getArchiveResults,
  pageMetadata,
  type AgentMessage,
  type AgentPlan,
} from './api';
import { AgentEmpty, AgentError, AgentTime, RawData } from './AgentParts';
import { ChatMessage } from './ChatPanel';
import { OperationPlan } from './OperationPlan';
import { indexToolResults } from './toolActivityData';

function ArchiveMessages({
  account,
  owner,
  active,
}: {
  account: string;
  owner: string;
  active: boolean;
}) {
  const { t } = useTranslation();
  const query = useQuery({
    queryKey: [...agentRoot('admin', account), 'archive-messages', owner],
    queryFn: ({ signal }) => getArchiveMessages(owner, '0', signal),
    enabled: active,
    retry: false,
  });
  const planQuery = useQuery({
    queryKey: [...agentRoot('admin', account), 'archive-plans', owner],
    queryFn: ({ signal }) => getArchivePlans(owner, '0', signal),
    enabled: active,
    retry: false,
  });
  const [olderPlans, setOlderPlans] = useState<AgentPlan[]>([]);
  const [older, setOlder] = useState<AgentMessage[]>([]);
  const [more, setMore] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const rows = [
    ...new Map([...older, ...(query.data ?? [])].map((row) => [row.id, row])).values(),
  ].sort((a, b) => (BigInt(a.id) < BigInt(b.id) ? -1 : 1));
  const plans = [
    ...new Map(
      [...olderPlans, ...(planQuery.data?.items ?? [])].map((row) => [row.id, row]),
    ).values(),
  ];
  const toolResults = indexToolResults(rows);
  const load = async () => {
    setBusy(true);
    setError(null);
    try {
      const before = plans.length
        ? plans.reduce((id, row) => (BigInt(row.id) < BigInt(id) ? row.id : id), plans[0]!.id)
        : '0';
      const [next, history] = await Promise.all([
        getArchiveMessages(owner, rows[0]?.id ?? '0'),
        getArchivePlans(owner, before),
      ]);
      setOlderPlans((previous) => [...history.items, ...previous]);
      setOlder((previous) => [...next, ...previous]);
      setMore(next.length === 100);
    } catch (failure) {
      setError(failure);
    } finally {
      setBusy(false);
    }
  };
  return query.isPending ? (
    <LoadingState />
  ) : query.error ? (
    <AgentError error={query.error} onRetry={() => void query.refetch()} />
  ) : (
    <div className="nb-stack">
      {more && query.data.length === 100 ? (
        <Button variant="secondary" disabled={busy} onClick={() => void load()}>
          {t('common.agent.loadOlder')}
        </Button>
      ) : null}
      {error ? <AgentError error={error} onRetry={() => void load()} /> : null}
      {planQuery.error ? (
        <AgentError error={planQuery.error} onRetry={() => void planQuery.refetch()} />
      ) : null}
      {rows.length ? (
        rows
          .filter((row) => !toolResults.consumed.has(row.id))
          .map((row) => (
            <div key={row.id} className={!row.active ? 'agent-archived' : ''}>
              <ChatMessage message={row} toolResults={toolResults.results.get(row.id)} />
            </div>
          ))
      ) : (
        <AgentEmpty body={t('common.agent.archiveEmpty')} />
      )}
      {plans.map((plan) => (
        <OperationPlan key={plan.id} role="admin" account={account} plan={plan} readOnly />
      ))}
    </div>
  );
}
function ArchiveResults({
  account,
  owner,
  active,
}: {
  account: string;
  owner: string;
  active: boolean;
}) {
  const { t } = useTranslation();
  const pager = useUrlPagePager({
    station: 'admin',
    listType: 'ai-archive-results',
    scopeKey: account + ':' + owner,
    pageParam: 'ai_result_page',
    pageSizeParam: 'ai_result_page_size',
  });
  const query = useQuery({
    queryKey: [
      ...agentRoot('admin', account),
      'archive-results',
      owner,
      pager.page,
      pager.pageSize,
    ],
    queryFn: ({ signal }) => getArchiveResults(owner, pager.page, pager.pageSize, signal),
    enabled: active,
    retry: false,
  });
  return query.isPending ? (
    <LoadingState />
  ) : query.error ? (
    <AgentError error={query.error} onRetry={() => void query.refetch()} />
  ) : (
    <>
      {query.data.items.length ? (
        query.data.items.map((row) => (
          <section key={row.request_id} className="nb-stack nb-stack--sm">
            <AgentTime value={row.created_at} />
            <RawData title={t('common.agent.modelResult')} value={row.message ?? row.error} />
          </section>
        ))
      ) : (
        <AgentEmpty body={t('common.agent.archiveEmpty')} />
      )}
      <PagePagination
        metadata={pageMetadata(query.data)}
        requestedPage={pager.page}
        busy={query.isFetching}
        onPageChange={pager.setPage}
        onPageSizeChange={pager.setPageSize}
      />
    </>
  );
}
export function ArchivesPanel({ account, active }: { account: string; active: boolean }) {
  const { t } = useTranslation();
  const [params, setParams] = useSearchState();
  const owner = params.get('ai_archive') ?? '';
  const [view, setView] = useState<'messages' | 'results'>('messages');
  const pager = useUrlPagePager({
    station: 'admin',
    listType: 'ai-archives',
    scopeKey: account,
    pageParam: 'ai_archive_page',
    pageSizeParam: 'ai_archive_page_size',
  });
  const query = useQuery({
    queryKey: [...agentRoot('admin', account), 'archives', pager.page, pager.pageSize],
    queryFn: ({ signal }) => getArchives(pager.page, pager.pageSize, signal),
    enabled: active,
    retry: false,
  });
  const select = (id: string) =>
    setParams((previous) => {
      const next = new URLSearchParams(previous);
      if (id) next.set('ai_archive', id);
      else next.delete('ai_archive');
      return next;
    });
  return (
    <Panel>
      <PanelHead title={t('common.agent.archives')} description={t('common.agent.archiveHelp')} />
      <PanelBody>
        {owner ? (
          <div className="nb-stack">
            <Button variant="secondary" onClick={() => select('')}>
              {t('common.agent.backList')}
            </Button>
            <Tabs
              label={t('common.agent.archives')}
              value={view}
              onChange={setView}
              tabs={[
                { value: 'messages', label: t('common.agent.originalMessages') },
                { value: 'results', label: t('common.agent.modelResults') },
              ]}
            />
            <div hidden={view !== 'messages'}>
              <ArchiveMessages
                key={owner}
                account={account}
                owner={owner}
                active={active && view === 'messages'}
              />
            </div>
            <div hidden={view !== 'results'}>
              <ArchiveResults
                key={owner}
                account={account}
                owner={owner}
                active={active && view === 'results'}
              />
            </div>
          </div>
        ) : query.isPending ? (
          <LoadingState />
        ) : query.error ? (
          <AgentError error={query.error} onRetry={() => void query.refetch()} />
        ) : (
          <>
            {query.data.items.length ? (
              <DataTable
                caption={t('common.agent.archives')}
                rows={query.data.items}
                rowKey={(row) => row.actor_id}
                columns={[
                  {
                    key: 'actor',
                    header: t('common.agent.actor'),
                    cell: 'title',
                    render: (row) => <span>{row.label || row.actor_id}</span>,
                  },
                  {
                    key: 'role',
                    header: t('common.agent.messageRole'),
                    cell: 'meta',
                    render: (row) => t(roleLabels[row.role] ?? 'common.agent.roles.steward'),
                  },
                  {
                    key: 'time',
                    header: t('common.agent.updated'),
                    cell: 'meta',
                    render: (row) => <AgentTime value={row.updated_at} />,
                  },
                  {
                    key: 'actions',
                    header: t('common.agent.actions'),
                    cell: 'action',
                    render: (row) => (
                      <Button variant="secondary" onClick={() => select(row.actor_id)}>
                        {t('common.agent.open')}
                      </Button>
                    ),
                  },
                ]}
              />
            ) : (
              <AgentEmpty body={t('common.agent.archiveEmpty')} />
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
  );
}
