import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { useSearchParams } from 'react-router';
import { Card, EmptyState, ErrorState, LoadingState, SectionHead } from '@shared/components/States';
import { Fold, Note } from '@shared/components/ui';
import { PagePagination } from '@shared/operations/PagePagination';
import { isPageNumber, isPageSize } from '@shared/operations/pageNumbers';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { formatCount } from '@shared/utils/formatNumber';
import type { RiskRole } from './api';
import { getSharedBrowsers } from './browserAPI';
import { BrowserAccounts } from './BrowserAccounts';

export function SharedBrowsers({ role, scopeKey }: { role: RiskRole; scopeKey: string }) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const stamp = useDateTimeFormatter();
  const [params, setParams] = useSearchParams();
  const rawPage = params.get('audit_browser_page');
  const page = isPageNumber(rawPage) ? rawPage : '1';
  const rawSize = Number(params.get('audit_browser_size'));
  const size = isPageSize(rawSize) ? rawSize : 20;
  const query = useQuery({
    queryKey: ['risk', role, scopeKey, 'shared-browsers', page, size],
    queryFn: ({ signal }) => getSharedBrowsers(role, page, size, signal),
    retry: false,
    gcTime: 0,
    staleTime: 0,
    refetchOnWindowFocus: false,
  });
  return (
    <div className="nb-stack">
      <SectionHead
        title={t('common.audit.browsers.title')}
        actions={
          <button
            type="button"
            className="nb-btn nb-btn--secondary"
            disabled={query.isFetching}
            onClick={() =>
              void Promise.all([
                query.refetch(),
                client.refetchQueries({
                  queryKey: ['risk', role, scopeKey, 'browser-accounts'],
                  type: 'active',
                }),
              ])
            }
          >
            {t('common.refresh')}
          </button>
        }
      />
      <Note>{t('common.audit.browsers.help')}</Note>
      {query.error ? (
        <ErrorState error={query.error} onRetry={() => void query.refetch()} />
      ) : query.data ? (
        <>
          {query.data.items.length === 0 ? (
            <EmptyState
              title={t('common.audit.browsers.empty')}
              body={t('common.audit.browsers.emptyHelp')}
            />
          ) : (
            query.data.items.map((group, index) => (
              <Card key={group.browser_id}>
                <SectionHead
                  title={t('common.audit.browsers.group', {
                    number: (Number(query.data.page) - 1) * query.data.page_size + index + 1,
                  })}
                  description={t('common.audit.browsers.accounts', {
                    total: formatCount(group.users).display,
                  })}
                />
                <p className="nb-small nb-muted">
                  {t('common.audit.browsers.first')}: {stamp(group.first_seen)}
                  <br />
                  {t('common.audit.browsers.latest')}: {stamp(group.last_seen)}
                </p>
                <Fold
                  plain
                  lazy
                  title={t('common.audit.browsers.viewAccounts')}
                  open={params.get('audit_browser') === group.browser_id}
                  onOpenChange={(open) =>
                    setParams((previous) => {
                      const next = new URLSearchParams(previous);
                      if (open) {
                        if (next.get('audit_browser') !== group.browser_id)
                          next.set('audit_browser_account_page', '1');
                        next.set('audit_browser', group.browser_id);
                      } else next.delete('audit_browser');
                      return next;
                    })
                  }
                >
                  {params.get('audit_browser') === group.browser_id ? (
                    <BrowserAccounts role={role} scopeKey={scopeKey} browserID={group.browser_id} />
                  ) : null}
                </Fold>
              </Card>
            ))
          )}
          <p className="nb-small nb-muted">{t('common.audit.browsers.observations')}</p>
          <div role="group" aria-label={t('common.audit.browsers.groupPages')}>
            <PagePagination
              metadata={query.data}
              requestedPage={page}
              alwaysShowJump
              busy={query.isFetching}
              onPageChange={(value) =>
                setParams((previous) => {
                  const next = new URLSearchParams(previous);
                  next.set('audit_browser_page', value);
                  next.delete('audit_browser');
                  return next;
                })
              }
              onPageSizeChange={(value) =>
                setParams((previous) => {
                  const next = new URLSearchParams(previous);
                  next.set('audit_browser_size', String(value));
                  next.set('audit_browser_page', '1');
                  next.delete('audit_browser');
                  return next;
                })
              }
            />
          </div>
        </>
      ) : (
        <LoadingState />
      )}
    </div>
  );
}
