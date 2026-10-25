import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { DonationUsesReviewAll, DonationUseReviewResults } from './DonationUsesPanelReview';
export { DonationUseReviewResults } from './DonationUsesPanelReview';
import { DonationKeyUseControls } from './DonationKeyUseControls';
import { Fold, Note } from './ui';
import { ErrorState, LoadingState } from './States';
import { PagePagination } from '@shared/operations/PagePagination';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import {
  getDonationUses,
  type DonationUse,
  type UseReviewResult,
  type UseRole,
  type UseVersion,
} from '@shared/operations/donationUses';
import type { ReviewedUseLabel } from '@shared/operations/donationUsesReview';
import { charityKeys } from '@shared/operations/charity';

export function DonationUsesPanel({
  role,
  accountId,
  donationId,
  donationKeyId,
  donationRevision,
  onReviewed,
  onPendingChange,
}: {
  role: UseRole;
  accountId: string;
  donationId: string;
  donationKeyId?: string;
  onReviewed?: (results: UseReviewResult[], labels: ReviewedUseLabel[]) => void;
  onPendingChange?: (pending: boolean) => void;
} & (
  | { role: 'owner'; donationRevision?: never }
  | { role: 'admin' | 'steward'; donationRevision: string }
)) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const root = role === 'admin' ? ['admin', 'operations'] : ['user'];
  const [locked, setLocked] = useState(false);
  const [receipt, setReceipt] = useState<{
    results: UseReviewResult[];
    labels: ReviewedUseLabel[];
  }>({ results: [], labels: [] });
  const query = useQuery({
    queryKey: [
      ...root,
      'donation-uses',
      accountId,
      donationId,
      donationKeyId ?? 'all',
      'materials',
    ],
    queryFn: async ({ signal }) => {
      const rows: DonationUse[] = [];
      let last = 1;
      for (let page = 1; page <= last; page++) {
        const value = await getDonationUses(
          role,
          donationId,
          String(page),
          100,
          '',
          signal,
          donationKeyId,
        );
        rows.push(...value.data);
        last = Math.min(19, Number(value.pagination.total_pages));
      }
      return rows;
    },
  });
  const groups = [
    ...new Map((query.data ?? []).map((row) => [row.donation_key_id, row])).keys(),
  ].map((id) => ({
    id,
    rows: (query.data ?? []).filter((row) => row.donation_key_id === id),
  }));
  const pager = useUrlPagePager({
    station: role === 'admin' ? 'admin' : 'user',
    listType: 'donation-materials',
    scopeKey: accountId + ':' + donationId + ':' + (donationKeyId ?? 'all'),
    pageParam: 'materials_page',
    pageSizeParam: 'materials_page_size',
  });
  const pageCount = Math.max(1, Math.ceil(groups.length / pager.pageSize));
  const page = Math.min(Number(pager.page), pageCount);
  const refresh = async () => {
    const keys =
      role === 'owner'
        ? [
            [...root, 'donation-uses', accountId, donationId],
            ['user', 'economy', 'donations'],
          ]
        : [
            [...root, 'donation-uses', accountId, donationId],
            charityKeys.donation(role, donationId),
            [...charityKeys.root(role), 'key-pages', accountId, donationId],
            [...charityKeys.root(role), 'donation-pages', accountId],
          ];
    await Promise.all(
      keys.map((queryKey) => client.invalidateQueries({ queryKey }, { throwOnError: true })),
    );
  };
  useEffect(() => {
    onPendingChange?.(locked);
    return () => onPendingChange?.(false);
  }, [locked, onPendingChange]);
  return (
    <div className="nb-stack">
      {role !== 'owner' && !donationKeyId ? (
        <DonationUsesReviewAll
          role={role}
          donationId={donationId}
          donationRevision={donationRevision}
          onLockedChange={setLocked}
          onReviewed={(results, labels) => {
            setReceipt({ results, labels });
            onReviewed?.(results, labels);
          }}
          refresh={refresh}
        />
      ) : null}
      {role === 'owner' && donationKeyId ? (
        <DonationKeyUseControls
          role={role}
          accountId={accountId}
          donationId={donationId}
          donationKeyId={donationKeyId}
          uses={query.data ?? []}
        />
      ) : null}
      {!onReviewed ? (
        <DonationUseReviewResults results={receipt.results} labels={receipt.labels} />
      ) : null}
      <Fold title={t('common.connections.donationMaterials')}>
        {query.isPending ? (
          <LoadingState />
        ) : query.error ? (
          <ErrorState error={query.error} onRetry={() => void query.refetch()} />
        ) : (
          groups.slice((page - 1) * pager.pageSize, page * pager.pageSize).map((group) => {
            const first = group.rows[0]!;
            const material = first.pending ?? first.approved ?? first.latest;
            const materialKey = (value: UseVersion) =>
              JSON.stringify([
                value.credential_id,
                value.credential_version,
                value.grant_revision,
                value.status,
                value.review_note,
                value.discovery_source_id,
                value.discovery_source_revision,
              ]);
            const versions = (kind: 'approved' | 'pending' | 'latest') => [
              ...new Map(
                group.rows.flatMap((row) =>
                  row[kind] ? ([[materialKey(row[kind]!), row[kind]!]] as const) : [],
                ),
              ).values(),
            ];
            return (
              <div key={group.id} className="nb-stack nb-stack--sm">
                <strong className="nb-mono">
                  {material?.display_head}…{material?.display_tail}
                </strong>
                {versions('approved').map((value) => (
                  <VersionMaterial
                    key={'approved:' + materialKey(value)}
                    label={t('common.connections.approvedVersion')}
                    value={value}
                    showDiscovery
                  />
                ))}
                {versions('pending').map((value) => (
                  <VersionMaterial
                    key={'pending:' + materialKey(value)}
                    label={t('common.connections.pendingVersion')}
                    value={value}
                    showDiscovery
                  />
                ))}
                {versions('latest')
                  .filter((value) => value.status !== 'pending' && value.status !== 'approved')
                  .map((value) => (
                    <VersionMaterial
                      key={'latest:' + materialKey(value)}
                      label={t('common.operations.charity.review')}
                      value={value}
                    />
                  ))}
              </div>
            );
          })
        )}
        <PagePagination
          metadata={{
            page: String(page),
            page_size: pager.pageSize,
            total_items: String(groups.length),
            total_pages: String(pageCount),
          }}
          requestedPage={pager.page}
          busy={query.isFetching || locked}
          onPageChange={pager.setPage}
          onPageSizeChange={pager.setPageSize}
        />
      </Fold>
    </div>
  );
}
function VersionMaterial({
  label,
  value,
  showDiscovery = true,
}: {
  label: string;
  value: UseVersion;
  showDiscovery?: boolean;
}) {
  const { t } = useTranslation();
  return (
    <div className="nb-stack nb-stack--sm">
      <h4>{label}</h4>
      <p className="nb-mono">
        {value.display_head}…{value.display_tail} ·{' '}
        {t('common.connections.versionNumber', { version: value.credential_version })}
      </p>
      <p>{t(`common.connections.useStates.${value.status}`)}</p>
      {value.review_note ? <Note>{value.review_note}</Note> : null}
      <p className="nb-sub">
        {value.review.required
          ? t('common.connections.bodyReviewRequired')
          : t('common.connections.bodyReviewNotRequired')}{' '}
        ·{' '}
        {value.review.material_available
          ? t('common.connections.reviewMaterialAvailable')
          : t('common.connections.reviewMaterialUnavailable')}
      </p>
      {showDiscovery && value.discovery ? (
        <p className="nb-sub">
          {t('common.connections.sharedDiscoveryState', {
            state: t(`common.connections.sourceStates.${value.discovery.state}`),
            count: value.discovery.count ?? '—',
          })}
        </p>
      ) : null}
    </div>
  );
}
