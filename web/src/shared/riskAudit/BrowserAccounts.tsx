import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { Link, useSearchParams } from 'react-router';
import { ErrorState, LoadingState } from '@shared/components/States';
import { DataTable } from '@shared/components/ui';
import { queryPath } from '@shared/operations/api';
import { PagePagination } from '@shared/operations/PagePagination';
import { isPageNumber, isPageSize } from '@shared/operations/pageNumbers';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import type { RiskRole } from './api';
import { getBrowserAccounts } from './browserAPI';

export function BrowserAccounts({
  role,
  scopeKey,
  browserID,
}: {
  role: RiskRole;
  scopeKey: string;
  browserID: string;
}) {
  const { t } = useTranslation();
  const stamp = useDateTimeFormatter();
  const [params, setParams] = useSearchParams();
  const rawPage = params.get('audit_browser_account_page');
  const page = isPageNumber(rawPage) ? rawPage : '1';
  const rawSize = Number(params.get('audit_browser_account_size'));
  const size = isPageSize(rawSize) ? rawSize : 20;
  const query = useQuery({
    queryKey: ['risk', role, scopeKey, 'browser-accounts', browserID, page, size],
    queryFn: ({ signal }) => getBrowserAccounts(role, browserID, page, size, signal),
    retry: false,
    gcTime: 0,
    staleTime: 0,
    refetchOnWindowFocus: false,
  });
  if (query.error) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />;
  if (!query.data) return <LoadingState />;
  return (
    <div className="nb-stack">
      <DataTable
        caption={t('common.audit.browsers.viewAccounts')}
        rows={query.data.associations}
        rowKey={(row) => row.user_id}
        columns={[
          {
            key: 'user',
            header: t('common.riskAudit.user'),
            cell: 'title',
            render: (row) => (
              <Link
                to={queryPath(role === 'admin' ? '/users' : '/steward', {
                  tab: role === 'steward' ? 'users' : undefined,
                  user: row.user_id,
                })}
              >
                {t('common.audit.selectedAccount', { user: row.user_id })}
              </Link>
            ),
          },
          {
            key: 'first',
            header: t('common.audit.browsers.first'),
            mobileLabel: t('common.audit.browsers.first'),
            cell: 'meta',
            render: (row) => stamp(row.first_seen),
          },
          {
            key: 'latest',
            header: t('common.audit.browsers.latest'),
            mobileLabel: t('common.audit.browsers.latest'),
            cell: 'meta',
            render: (row) => stamp(row.last_seen),
          },
        ]}
      />
      <div role="group" aria-label={t('common.audit.browsers.accountPages')}>
        <PagePagination
          metadata={query.data}
          requestedPage={page}
          alwaysShowJump
          busy={query.isFetching}
          onPageChange={(value) =>
            setParams((previous) => {
              const next = new URLSearchParams(previous);
              next.set('audit_browser_account_page', value);
              return next;
            })
          }
          onPageSizeChange={(value) =>
            setParams((previous) => {
              const next = new URLSearchParams(previous);
              next.set('audit_browser_account_size', String(value));
              next.set('audit_browser_account_page', '1');
              return next;
            })
          }
        />
      </div>
    </div>
  );
}
