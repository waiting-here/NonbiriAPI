import { useEffect, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { charityKeys } from '@shared/operations/charity';
import { getDonationHandlingPage } from '@shared/operations/donationHandlingPages';
import { managementResourceID } from '@shared/operations/charityModelPages';
import { useSearchState } from '@shared/operations/useSearchState';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { PagePagination } from '@shared/operations/PagePagination';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { DonationHandlingControl } from '../DonationHandling';
import { EmptyState, ErrorState, LoadingState, PageHeader } from '../States';
import { Button, DataTable, Field, FilterBar } from '../ui';

export function DonationHandlingPanel({
  accountId,
  onCapabilityLoss,
}: {
  accountId: string;
  onCapabilityLoss?: () => void;
}) {
  const { t } = useTranslation();
  const time = useDateTimeFormatter();
  const client = useQueryClient();
  const [params, setParams] = useSearchState();
  const raw = params.get('handling_view');
  const handling = raw === 'processed' || raw === 'all' ? raw : 'pending';
  const exact = params.get('handling_donation') ?? '';
  const donationID = managementResourceID(exact) ? exact : '';
  const [draftState, setDraft] = useState({ source: donationID, value: donationID });
  const draft = draftState.source === donationID ? draftState.value : donationID;
  const pager = useUrlPagePager({
    station: 'user',
    listType: 'donation-handling',
    scopeKey: accountId,
    pageParam: 'handling_page',
    pageSizeParam: 'handling_page_size',
  });
  const root = [...charityKeys.root('steward'), 'handling-summaries', accountId] as const;
  const query = useQuery({
    queryKey: [...root, handling, donationID, pager.page, pager.pageSize],
    queryFn: ({ signal }) =>
      getDonationHandlingPage(handling, donationID, pager.page, pager.pageSize, signal),
    retry: false,
  });
  const lost = isForbidden(query.error) || isUnauthorized(query.error);
  useEffect(() => {
    if (lost) onCapabilityLoss?.();
  }, [lost, onCapabilityLoss]);
  const change = (view: string, id: string) =>
    setParams((current) => {
      const next = new URLSearchParams(current);
      next.set('handling_view', view);
      next.delete('handling_donation');
      if (id) next.set('handling_donation', id);
      next.set('handling_page', '1');
      return next;
    });
  if (lost) return <p role="alert">{t('common.operations.charity.accessLost')}</p>;
  return (
    <div className="nb-form">
      <PageHeader
        title={t('common.workflow.approvedDonations')}
        description={t('common.workflow.handlingHelp')}
      />
      <FilterBar
        activeCount={handling !== 'pending' || Boolean(donationID) ? 1 : 0}
        secondaryAlwaysVisible
        secondaryLabel={t('common.workflow.filters')}
        onSubmit={() => {
          if (!draft || managementResourceID(draft)) change(handling, draft);
        }}
        search={
          <>
            <Field label={t('common.workflow.exactDonation')}>
              {(props) => (
                <input
                  {...props}
                  inputMode="numeric"
                  value={draft}
                  placeholder={t('common.workflow.exactDonationPlaceholder')}
                  onChange={(event) => setDraft({ source: donationID, value: event.target.value })}
                />
              )}
            </Field>
            <Button type="submit" disabled={Boolean(draft) && !managementResourceID(draft)}>
              {t('common.search')}
            </Button>
          </>
        }
        secondary={
          <Field label={t('common.donationHandling.filter')}>
            {(props) => (
              <select
                {...props}
                value={handling}
                onChange={(event) => change(event.target.value, donationID)}
              >
                <option value="pending">{t('common.donationHandling.states.pending')}</option>
                <option value="processed">{t('common.donationHandling.states.processed')}</option>
                <option value="all">{t('common.all')}</option>
              </select>
            )}
          </Field>
        }
      />
      {query.error ? <ErrorState error={query.error} onRetry={() => void query.refetch()} /> : null}
      {query.isPending ? (
        <LoadingState />
      ) : query.data ? (
        <>
          {query.data.data.length ? (
            <DataTable
              caption={t('common.workflow.approvedDonations')}
              rowKey={(row) => row.donation_id}
              rows={query.data.data}
              columns={[
                {
                  key: 'id',
                  header: t('common.workflow.donation'),
                  cell: 'title',
                  render: (row) =>
                    t('common.operations.charity.donationNumber', { id: row.donation_id }),
                },
                {
                  key: 'created',
                  header: t('common.workflow.created'),
                  cell: 'meta',
                  render: (row) => time(row.created_at),
                },
                {
                  key: 'keys',
                  header: t('common.workflow.keySummary'),
                  cell: 'meta',
                  render: (row) =>
                    t('common.workflow.keyCounts', {
                      all: row.key_count,
                      bound: row.bound_key_count,
                    }),
                },
                {
                  key: 'process',
                  header: t('common.workflow.actions'),
                  cell: 'action',
                  render: (row) => (
                    <DonationHandlingControl
                      donationID={row.donation_id}
                      role="steward"
                      handling={row.handling}
                      keyCount={row.key_count}
                      disabled={query.isFetching || Boolean(query.error)}
                      refresh={() =>
                        client.invalidateQueries({ queryKey: root }, { throwOnError: true })
                      }
                      onCapabilityLoss={onCapabilityLoss}
                    />
                  ),
                },
              ]}
            />
          ) : (
            <EmptyState
              title={t('common.workflow.noHandling')}
              body={t('common.workflow.handlingEmpty')}
              action={
                handling === 'pending' ? (
                  <Button type="button" onClick={() => change('processed', donationID)}>
                    {t('common.workflow.viewProcessed')}
                  </Button>
                ) : undefined
              }
            />
          )}
          <PagePagination
            metadata={query.data.pagination}
            requestedPage={pager.page}
            onPageChange={pager.setPage}
            onPageSizeChange={pager.setPageSize}
            busy={query.isFetching}
          />
        </>
      ) : null}
    </div>
  );
}
