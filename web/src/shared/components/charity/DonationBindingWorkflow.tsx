import { useEffect, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { charityKeys, type CharityRole } from '@shared/operations/charity';
import {
  getManagedCharityModel,
  getManagedCharityModelsPage,
  validManagementSearch,
} from '@shared/operations/charityModelPages';
import type { ManagedDonationKeySummary } from '@shared/operations/donationPages';
import { usePagePager } from '@shared/operations/usePagePager';
import { useDetailNavigation } from '@shared/operations/useDetailNavigation';
import { PagePagination } from '@shared/operations/PagePagination';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import { ConfirmDialog } from '../ConfirmDialog';
import { EmptyState, ErrorState, LoadingState, PageHeader, StatusBadge } from '../States';
import { Button, DataTable, Field, FilterBar } from '../ui';
import { BindingAddForm } from './BindingAddForm';
import { PriceConfigBrief } from './PriceConfigSummary';

export function DonationBindingWorkflow({
  role,
  accountId,
  item,
  onBack,
  onCreate,
  initialTarget = '',
  onCapabilityLoss,
}: {
  role: CharityRole;
  accountId: string;
  item: ManagedDonationKeySummary;
  onBack: () => void;
  onCreate: () => void;
  initialTarget?: string;
  onCapabilityLoss?: () => void;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const [target, setTarget] = useState(initialTarget);
  const [selectionCount, setSelectionCount] = useState(0);
  const [confirmChange, setConfirmChange] = useState(false);
  const [q, setQ] = useState('');
  const [draft, setDraft] = useState('');
  const [enabled, setEnabled] = useState('');
  const pager = usePagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: 'donation-binding-targets',
    scopeKey: `${accountId}:${item.key_id}`,
    resetKey: `${q}:${enabled}`,
  });
  const models = useQuery({
    queryKey: [
      ...charityKeys.root(role),
      'binding-targets',
      accountId,
      q,
      enabled,
      pager.page,
      pager.pageSize,
    ],
    queryFn: ({ signal }) =>
      getManagedCharityModelsPage(role, q, enabled, pager.page, pager.pageSize, signal),
    enabled: !target,
    retry: false,
  });
  const detail = useQuery({
    queryKey: [...charityKeys.root(role), 'binding-target', accountId, target],
    queryFn: ({ signal }) => getManagedCharityModel(role, target, signal),
    enabled: Boolean(target),
    retry: false,
  });
  const lost = [models.error, detail.error].some(
    (error) => isForbidden(error) || isUnauthorized(error),
  );
  const { listRef, detailRef, remember } = useDetailNavigation(
    target,
    target ? !detail.isPending && !detail.isFetching : !models.isPending && !models.isFetching,
  );
  useEffect(() => {
    if (lost) onCapabilityLoss?.();
  }, [lost, onCapabilityLoss]);
  const changeTarget = () => {
    setTarget('');
    setSelectionCount(0);
  };
  if (lost) return <p role="alert">{t('common.operations.charity.accessLost')}</p>;
  return (
    <div className="nb-form" ref={listRef} tabIndex={-1}>
      <PageHeader
        title={t('common.workflow.addConnections')}
        description={t('common.workflow.fixedKey', {
          donation: item.donation_id,
          head: item.display_head,
          tail: item.display_tail,
        })}
        back={
          <Button type="button" variant="ghost" onClick={onBack}>
            {t('common.workflow.returnDonation')}
          </Button>
        }
        actions={
          target ? (
            <Button
              type="button"
              onClick={() => (selectionCount ? setConfirmChange(true) : changeTarget())}
            >
              {t('common.workflow.changeTarget', { count: selectionCount })}
            </Button>
          ) : (
            <Button type="button" onClick={onCreate}>
              {t('common.workflow.createThenBind')}
            </Button>
          )
        }
      />
      <p>
        {t('common.operations.charity.charityStateLabel', {
          state: t(`common.operations.charity.charityState.${item.charity_state}`),
        })}
      </p>
      {target ? (
        detail.isPending ? (
          <LoadingState />
        ) : detail.error ? (
          <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />
        ) : detail.data ? (
          <div ref={detailRef} tabIndex={-1}>
            <BindingAddForm
              key={`${target}:${item.key_id}`}
              role={role}
              accountId={accountId}
              model={detail.data}
              fixedKey={item}
              refresh={() =>
                client.invalidateQueries(
                  { queryKey: charityKeys.root(role) },
                  { throwOnError: true },
                )
              }
              onCapabilityLoss={onCapabilityLoss}
              onSelectionCount={setSelectionCount}
            />
          </div>
        ) : null
      ) : (
        <>
          <FilterBar
            activeCount={[q, enabled].filter(Boolean).length}
            secondaryAlwaysVisible
            secondaryLabel={t('common.workflow.filters')}
            onSubmit={() => {
              if (validManagementSearch(draft)) setQ(draft.trim());
            }}
            search={
              <>
                <Field label={t('common.operations.charity.searchModels')}>
                  {(props) => (
                    <input
                      {...props}
                      type="search"
                      placeholder={t('common.workflow.modelSearch')}
                      value={draft}
                      onChange={(event) => setDraft(event.target.value)}
                    />
                  )}
                </Field>
                <Button type="submit" disabled={!validManagementSearch(draft)}>
                  {t('common.search')}
                </Button>
              </>
            }
            secondary={
              <Field label={t('common.workflow.modelState')}>
                {(props) => (
                  <select
                    {...props}
                    value={enabled}
                    onChange={(event) => setEnabled(event.target.value)}
                  >
                    <option value="">{t('common.all')}</option>
                    <option value="true">{t('common.enabled')}</option>
                    <option value="false">{t('common.disabled')}</option>
                  </select>
                )}
              </Field>
            }
          />
          {models.isPending ? (
            <LoadingState />
          ) : models.error ? (
            <ErrorState error={models.error} onRetry={() => void models.refetch()} />
          ) : (
            <>
              {models.data.data.length ? (
                <DataTable
                  caption={t('common.workflow.chooseTarget')}
                  rowKey={(row) => row.id}
                  rows={models.data.data}
                  columns={[
                    {
                      key: 'model',
                      header: t('common.workflow.model'),
                      cell: 'title',
                      render: (row) => (
                        <strong className="mono nb-workflow-id">{row.full_name}</strong>
                      ),
                    },
                    {
                      key: 'state',
                      header: t('common.operations.charity.state'),
                      cell: 'status',
                      render: (row) => (
                        <StatusBadge
                          active={row.enabled}
                          label={t(row.enabled ? 'common.enabled' : 'common.disabled')}
                        />
                      ),
                    },
                    {
                      key: 'bindings',
                      header: t('common.workflow.connections'),
                      cell: 'meta',
                      align: 'num',
                      render: (row) => row.binding_count,
                    },
                    {
                      key: 'price',
                      header: t('common.operations.charity.pricing'),
                      cell: 'meta',
                      render: (row) => <PriceConfigBrief config={row} role={role} />,
                    },
                    {
                      key: 'select',
                      header: t('common.workflow.actions'),
                      cell: 'action',
                      render: (row) => (
                        <Button
                          type="button"
                          disabled={models.isFetching}
                          onClick={() => {
                            remember();
                            setTarget(row.id);
                          }}
                        >
                          {t('common.workflow.chooseTarget')}
                        </Button>
                      ),
                    },
                  ]}
                />
              ) : (
                <EmptyState
                  title={t('common.workflow.noModels')}
                  body={t('common.workflow.trySearch')}
                />
              )}
              <PagePagination
                metadata={models.data.pagination}
                requestedPage={pager.page}
                onPageChange={pager.setPage}
                onPageSizeChange={pager.setPageSize}
                busy={models.isFetching}
              />
            </>
          )}
        </>
      )}
      <ConfirmDialog
        open={confirmChange}
        title={t('common.workflow.changeTarget', { count: selectionCount })}
        description={t('common.workflow.clearForTarget', { count: selectionCount })}
        confirmLabel={t('common.workflow.changeAndClear', { count: selectionCount })}
        onCancel={() => setConfirmChange(false)}
        onConfirm={() => {
          setConfirmChange(false);
          changeTarget();
        }}
      />
    </div>
  );
}
