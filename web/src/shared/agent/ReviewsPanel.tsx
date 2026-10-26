import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { useSearchState } from '@shared/operations/useSearchState';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { PagePagination } from '@shared/operations/PagePagination';
import { LoadingState } from '@shared/components/States';
import { formatCount } from '@shared/utils/formatNumber';
import { Button, DataTable, Panel, PanelBody, PanelHead } from '@shared/components/ui';
import { agentRoot, getReviews, getReview, pageMetadata, type AgentRole } from './api';
import {
  AgentEmpty,
  AgentError,
  AgentMarkdown,
  AgentState,
  AgentTime,
  RawData,
} from './AgentParts';

export function ReviewsPanel({
  role,
  account,
  active,
}: {
  role: AgentRole;
  account: string;
  active: boolean;
}) {
  const { t } = useTranslation();
  const [params, setParams] = useSearchState();
  const selected = params.get('ai_review') ?? '';
  const pager = useUrlPagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: 'ai-reviews',
    scopeKey: account,
    pageParam: 'ai_review_page',
    pageSizeParam: 'ai_review_page_size',
  });
  const query = useQuery({
    queryKey: [...agentRoot(role, account), 'reviews', pager.page, pager.pageSize],
    queryFn: ({ signal }) => getReviews(role, pager.page, pager.pageSize, signal),
    enabled: active,
    retry: false,
    refetchInterval: ({ state }) =>
      state.data?.items.some((row) => ['pending', 'running'].includes(row.job.state))
        ? 3000
        : false,
  });
  const detail = useQuery({
    queryKey: [...agentRoot(role, account), 'review', selected],
    queryFn: ({ signal }) => getReview(role, selected, signal),
    enabled: active && Boolean(selected),
    retry: false,
    refetchInterval: ({ state }) =>
      state.data && ['pending', 'running'].includes(state.data.job.state) ? 2000 : false,
  });
  const select = (id: string) =>
    setParams((previous) => {
      const next = new URLSearchParams(previous);
      if (id) next.set('ai_review', id);
      else next.delete('ai_review');
      return next;
    });
  return (
    <Panel>
      <PanelHead title={t('common.agent.reviews')} description={t('common.agent.reviewsHelp')} />
      <PanelBody>
        {selected ? (
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
                <AgentState state={detail.data.job.state} />
                {['pending', 'running'].includes(detail.data.job.state) ? (
                  <p className="agent-number">
                    {t('common.agent.queue', {
                      position: formatCount(detail.data.job.queue.position).display,
                      length: formatCount(detail.data.job.queue.length).display,
                    })}
                  </p>
                ) : null}
                {detail.data.result ? (
                  <>
                    <h3>{t('common.agent.decision')}</h3>
                    <p>
                      {t(
                        (
                          {
                            approve: 'common.agent.decisions.approve',
                            reject: 'common.agent.decisions.reject',
                            manual: 'common.agent.decisions.manual',
                          } as Record<string, string>
                        )[detail.data.result.decision] ?? 'common.agent.decisions.manual',
                      )}
                    </p>
                    <h3>{t('common.agent.internalReason')}</h3>
                    <AgentMarkdown>{detail.data.result.internal_reason}</AgentMarkdown>
                    <h3>{t('common.agent.userNote')}</h3>
                    <AgentMarkdown>{detail.data.result.user_note}</AgentMarkdown>
                    <RawData
                      title={t('common.agent.bindingProposal')}
                      value={detail.data.result.bindings}
                    />
                  </>
                ) : null}
                <RawData
                  title={t('common.agent.executionResult')}
                  value={detail.data.execution ?? detail.data.job.error}
                />
                <RawData title={t('common.agent.snapshot')} value={detail.data.intelligence} />
                {detail.data.turns?.map((row) => (
                  <RawData
                    key={row.request_id}
                    title={t('common.agent.modelResult')}
                    value={row.message ?? row.error}
                  />
                ))}
              </>
            )}
          </div>
        ) : query.isPending ? (
          <LoadingState />
        ) : query.error ? (
          <AgentError error={query.error} onRetry={() => void query.refetch()} />
        ) : (
          <>
            {!query.data.items.length ? (
              <AgentEmpty body={t('common.agent.reviewsEmpty')} />
            ) : (
              <DataTable
                caption={t('common.agent.reviews')}
                rows={query.data.items}
                rowKey={(row) => row.job.id}
                columns={[
                  {
                    key: 'donation',
                    header: t('common.agent.donation'),
                    cell: 'title',
                    render: (row) => <span className="nb-mono">{row.job.donation_id}</span>,
                  },
                  {
                    key: 'state',
                    header: t('common.agent.status'),
                    cell: 'status',
                    render: (row) => <AgentState state={row.job.state} />,
                  },
                  {
                    key: 'time',
                    header: t('common.agent.created'),
                    cell: 'meta',
                    render: (row) => <AgentTime value={row.job.created_at} />,
                  },
                  {
                    key: 'open',
                    header: t('common.agent.actions'),
                    cell: 'action',
                    render: (row) => (
                      <Button variant="secondary" onClick={() => select(row.job.id)}>
                        {t('common.agent.open')}
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
  );
}
