import { useEffect, useState } from 'react';
import { Link, useLocation } from 'react-router';
import { useTranslation } from 'react-i18next';
import { useQuery } from '@tanstack/react-query';
import { DataTable, Note, Tabs } from '@shared/components/ui';
import { NativeServicesSummary } from '@shared/components/ServiceRoutesSummary';
import { PagePagination } from '@shared/operations/PagePagination';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { formatCount } from '@shared/utils/formatNumber';
import { CoreEmpty, CoreErrorPanel, CoreLoading } from '../components';
import { connectionAPI } from './api';
import { connectionKeys } from './ResourceList';
import type { ImpactKind } from './types';

export function CredentialImpactPanel({
  accountId,
  credentialId,
  revision,
  onReady,
}: {
  accountId: string;
  credentialId: string;
  revision: string;
  onReady?: (revision: string) => void;
}) {
  const { t } = useTranslation();
  const location = useLocation();
  const [kind, setKind] = useState<ImpactKind>('personal');
  const pager = useUrlPagePager({
    station: 'user',
    listType: 'credential-impact',
    scopeKey: accountId + ':' + credentialId,
    resetKey: kind,
    pageParam: 'impact_page',
    pageSizeParam: 'impact_page_size',
  });
  const query = useQuery({
    queryKey: [
      ...connectionKeys.root(accountId),
      'impact',
      credentialId,
      revision,
      kind,
      pager.page,
      pager.pageSize,
    ],
    queryFn: ({ signal }) => connectionAPI.impact(credentialId, kind, pager, signal),
  });
  useEffect(() => {
    if (query.data && !query.error) onReady?.(revision);
  }, [query.data, query.error, revision, onReady]);
  return (
    <div className="nb-stack">
      <Tabs
        label={t('common.connections.impact')}
        value={kind}
        tabs={[
          { value: 'personal', label: t('common.connections.personalUses') },
          { value: 'charity', label: t('common.connections.charityUses') },
          { value: 'discovery', label: t('common.connections.discoverySources') },
        ]}
        onChange={setKind}
      />
      <Note>{t('common.connections.separateLimits')}</Note>
      {query.isPending ? (
        <CoreLoading />
      ) : query.error ? (
        <CoreErrorPanel error={query.error} onRetry={() => void query.refetch()} />
      ) : query.data ? (
        <>
          <p className="nb-sub">
            {t('common.connections.impactCounts', {
              keys: formatCount(query.data.summary.endpoint_keys).display,
              donations: formatCount(query.data.summary.donation_keys).display,
              sources: formatCount(query.data.summary.discovery_sources).display,
            })}
          </p>
          {query.data.data.length ? (
            <DataTable
              caption={t('common.connections.impact')}
              rowKey={(row) =>
                [
                  row.state,
                  row.endpoint_key_id,
                  row.use_id,
                  row.discovery_source_id,
                  row.credential_version,
                ].join(':')
              }
              rows={query.data.data}
              columns={[
                {
                  key: 'version',
                  header: t('common.connections.version'),
                  cell: 'title',
                  render: (row) =>
                    t('common.connections.versionNumber', { version: row.credential_version }),
                },
                {
                  key: 'services',
                  header: t('common.nativeServices.title'),
                  cell: 'meta',
                  render: (row) => <NativeServicesSummary value={row.native_services} />,
                },
                {
                  key: 'state',
                  header: t('common.connections.status'),
                  cell: 'status',
                  render: (row) =>
                    row.state === 'current'
                      ? t('common.connections.current')
                      : row.state === 'approved'
                        ? t('common.connections.approved')
                        : t('common.connections.pending'),
                },
                {
                  key: 'details',
                  header: t('common.connections.actions'),
                  cell: 'action',
                  render: (row) => (
                    <Link
                      className="nb-btn nb-btn--secondary"
                      to={
                        row.donation_id
                          ? '/charity/donations/' + row.donation_id
                          : row.discovery_source_id
                            ? '/endpoints?discovery_source_id=' + row.discovery_source_id
                            : '/endpoints/' +
                              row.endpoint_id +
                              '?focus_key_id=' +
                              row.endpoint_key_id
                      }
                      state={{ returnTo: location.pathname + location.search }}
                    >
                      {t('common.connections.viewScope')}
                    </Link>
                  ),
                },
              ]}
            />
          ) : (
            <CoreEmpty
              title={t('common.connections.noImpact')}
              body={t('common.connections.noImpactBody')}
            />
          )}
          <PagePagination
            metadata={query.data.pagination}
            requestedPage={pager.page}
            busy={query.isFetching}
            onPageChange={pager.setPage}
            onPageSizeChange={pager.setPageSize}
          />
        </>
      ) : null}
    </div>
  );
}
