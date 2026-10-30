import { useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Card, EmptyState, ErrorState, LoadingState, SectionHead } from './States';
import { Button, DataTable, Field, FilterBar, Note } from './ui';
import { MarkdownText } from './MarkdownText';
import { DonationStatusBadge } from './UseSummary';
import { DonationHandlingStatus } from './DonationHandling';
import { PagePagination } from '@shared/operations/PagePagination';
import { usePagePager } from '@shared/operations/usePagePager';
import { managementRoot } from '@shared/operations/managedUsers';
import {
  DONATION_DISPLAY_STATUSES,
  type DonationDisplayStatus,
} from '@shared/operations/donationUses';
import {
  getUserDonationHistoryDetail,
  getUserDonationHistoryPage,
  type UserDonationHistoryItem,
} from '@shared/operations/userDonationHistory';
import type { DonationPageRole } from '@shared/operations/donationPages';
import { validManagementSearch } from '@shared/operations/charityModelPages';
import { isApiError, isForbidden, isUnauthorized } from '@shared/query/http';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { formatCount } from '@shared/utils/formatNumber';

export interface UserDonationHistoryProps {
  role: DonationPageRole;
  accountId: string;
  userId: string;
  enabled: boolean;
  onCapabilityLoss?: () => void;
}

export function UserDonationHistory({
  role,
  accountId,
  userId,
  enabled,
  onCapabilityLoss,
}: UserDonationHistoryProps) {
  const { t } = useTranslation();
  const formatDateTime = useDateTimeFormatter();
  const [query, setQuery] = useState('');
  const [draft, setDraft] = useState('');
  const [status, setStatus] = useState<DonationDisplayStatus | ''>('');
  const [selection, setSelection] = useState({ userId, id: '' });
  const selected = selection.userId === userId ? selection.id : '';
  const pager = usePagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: 'user-donation-history',
    scopeKey: `${accountId}:${userId}`,
    resetKey: `${query}:${status}`,
  });
  const list = useQuery({
    queryKey: [
      ...managementRoot(role),
      'user-donation-history',
      accountId,
      userId,
      query,
      status,
      pager.page,
      pager.pageSize,
    ],
    queryFn: ({ signal }) =>
      getUserDonationHistoryPage(
        role,
        userId,
        { q: query, review_status: status },
        pager.page,
        pager.pageSize,
        signal,
      ),
    enabled,
    retry: false,
  });
  const detail = useQuery({
    queryKey: [
      ...managementRoot(role),
      'user-donation-history-detail',
      accountId,
      userId,
      selected,
    ],
    queryFn: ({ signal }) => getUserDonationHistoryDetail(role, userId, selected, signal),
    enabled: enabled && Boolean(selected),
    retry: false,
  });
  const denied = [list.error, detail.error].some(
    (error) =>
      isUnauthorized(error) ||
      (isForbidden(error) && !(isApiError(error) && error.code === 'steward_permission_denied')),
  );
  useEffect(() => {
    if (denied) onCapabilityLoss?.();
  }, [denied, onCapabilityLoss]);
  const reset = () => {
    setDraft('');
    setQuery('');
    setStatus('');
  };
  const title = t('common.donationHistory.title');
  const view = (row: UserDonationHistoryItem) => (
    <div className="nb-stack nb-stack--sm">
      <strong>{row.description || t('common.operations.charity.noDescription')}</strong>
      {row.owner_hidden_at !== null ? <small>{t('common.donationHistory.hidden')}</small> : null}
    </div>
  );
  return (
    <div className="nb-stack">
      <SectionHead title={title} description={t('common.donationHistory.description')} />
      {!enabled || denied ? (
        <Note tone="warn">{t('common.donationHistory.denied')}</Note>
      ) : (
        <>
          <FilterBar
            onReset={reset}
            activeCount={[query, status].filter(Boolean).length}
            search={
              <>
                <Field
                  label={t('common.donationHandling.search')}
                  error={
                    validManagementSearch(draft)
                      ? undefined
                      : t('common.donationHandling.searchInvalid')
                  }
                >
                  {(field) => (
                    <input
                      {...field}
                      type="search"
                      placeholder={t('common.donationHandling.searchPlaceholder')}
                      value={draft}
                      onChange={(event) => setDraft(event.target.value)}
                    />
                  )}
                </Field>
              </>
            }
            secondary={
              <Field label={t('common.donationHistory.status')}>
                {(field) => (
                  <select
                    {...field}
                    value={status}
                    onChange={(event) =>
                      setStatus(event.target.value as DonationDisplayStatus | '')
                    }
                  >
                    <option value="">{t('common.donationHistory.allStatuses')}</option>
                    {DONATION_DISPLAY_STATUSES.map((value) => (
                      <option value={value} key={value}>
                        {t(`common.connections.donationStates.${value}`)}
                      </option>
                    ))}
                  </select>
                )}
              </Field>
            }
            chips={[
              ...(query
                ? [
                    {
                      key: 'q',
                      label: query,
                      onRemove: () => {
                        setQuery('');
                        setDraft('');
                      },
                      removeLabel: t('common.resetFilter'),
                    },
                  ]
                : []),
              ...(status
                ? [
                    {
                      key: 'status',
                      label: t(`common.connections.donationStates.${status}`),
                      onRemove: () => setStatus(''),
                      removeLabel: t('common.resetFilter'),
                    },
                  ]
                : []),
            ]}
            clearAllLabel={t('common.resetFilter')}
            onClearAll={reset}
            onSubmit={() => {
              if (validManagementSearch(draft)) setQuery(draft);
            }}
          />
          {list.isPending ? (
            <LoadingState />
          ) : list.error ? (
            <ErrorState error={list.error} onRetry={() => void list.refetch()} />
          ) : list.data ? (
            <>
              {list.data.data.length === 0 ? (
                <EmptyState
                  title={t('common.donationHistory.empty')}
                  body={t('common.donationHistory.emptyBody')}
                  action={
                    <Button variant="ghost" onClick={reset}>
                      {t('common.resetFilter')}
                    </Button>
                  }
                />
              ) : (
                <DataTable
                  caption={title}
                  rows={list.data.data}
                  rowKey={(row) => row.id}
                  selectedKey={selected}
                  columns={[
                    {
                      key: 'description',
                      header: t('common.donationHistory.donation'),
                      cell: 'title',
                      render: view,
                    },
                    {
                      key: 'status',
                      header: t('common.donationHistory.status'),
                      cell: 'status',
                      render: (row) => (
                        <DonationStatusBadge status={row.status} summary={row.use_summary} />
                      ),
                    },
                    {
                      key: 'created',
                      header: t('common.donationHistory.created'),
                      cell: 'meta',
                      render: (row) => formatDateTime(row.created_at * 1000),
                    },
                    {
                      key: 'actions',
                      header: t('common.workflow.actions'),
                      cell: 'action',
                      render: (row) => (
                        <Button
                          size="sm"
                          variant="secondary"
                          onClick={() => setSelection({ userId, id: row.id })}
                        >
                          {t('management.users.view')}
                        </Button>
                      ),
                    },
                  ]}
                />
              )}
              <PagePagination
                metadata={list.data.pagination}
                requestedPage={pager.page}
                onPageChange={pager.setPage}
                onPageSizeChange={pager.setPageSize}
                busy={list.isFetching}
              />
            </>
          ) : null}
          {selected ? (
            <Card>
              <div className="nb-actions">
                <Button variant="ghost" onClick={() => setSelection({ userId, id: '' })}>
                  {t('common.donationHistory.back')}
                </Button>
              </div>
              {detail.isPending ? (
                <LoadingState />
              ) : detail.error ? (
                <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />
              ) : detail.data ? (
                <div className="nb-stack">
                  <h3>{t('common.donationHistory.detail')}</h3>
                  <DonationStatusBadge
                    status={detail.data.status}
                    summary={detail.data.use_summary}
                  />
                  <MarkdownText>
                    {detail.data.description || t('common.operations.charity.noDescription')}
                  </MarkdownText>
                  <DonationHandlingStatus handling={detail.data.handling} />
                  <dl className="detail-grid">
                    <dt>{t('common.donationHistory.created')}</dt>
                    <dd>{formatDateTime(detail.data.created_at * 1000)}</dd>
                    <dt>{t('common.donationHistory.updated')}</dt>
                    <dd>{formatDateTime(detail.data.updated_at * 1000)}</dd>
                    <dt>{t('common.donationHistory.keys')}</dt>
                    <dd>{formatCount(detail.data.key_count).display}</dd>
                    <dt>{t('common.donationHistory.sources')}</dt>
                    <dd>{formatCount(detail.data.source_count).display}</dd>
                    <dt>{t('common.donationHistory.visibility')}</dt>
                    <dd>
                      {t(
                        detail.data.owner_hidden_at === null
                          ? 'common.donationHistory.visible'
                          : 'common.donationHistory.hidden',
                      )}
                    </dd>
                  </dl>
                  {detail.data.review_result?.reason ? (
                    <MarkdownText>{detail.data.review_result.reason}</MarkdownText>
                  ) : null}
                </div>
              ) : null}
            </Card>
          ) : null}
        </>
      )}
    </div>
  );
}
