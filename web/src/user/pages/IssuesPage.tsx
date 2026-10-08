import { Tabs } from '@shared/components/ui/Tabs';
import { RecordsHeader } from '../components/RecordsHeader';
import { Link } from 'react-router';
import { useSearchState } from '@shared/operations/useSearchState';
import { useTranslation } from 'react-i18next';
import { Card, EmptyState, ErrorState, LoadingState, StatusBadge } from '@shared/components/States';
import { PagePagination } from '@shared/operations/PagePagination';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { formatDateTime } from '@shared/utils/datetime';
import { useUserAuthority, type Issue } from '../features/operations/data';
import { useIssuePage, type IssueState } from '../features/operations/issuePage';
import '@shared/operations/operations.css';

// Stable list identity; account IDs belong in scopeKey, never in this value.
const ISSUES_LIST_TYPE = 'issues';
const ISSUE_STATE_PARAM = 'state';

const SUMMARY_LABEL_KEYS: Record<Issue['summary_code'], string> = {
  discovery_failed: 'user.issues.summary.discoveryFailed',
  no_routable_binding: 'user.issues.summary.noRoutableBinding',
  credential_invalid: 'user.issues.summary.credentialInvalid',
  configuration_invalid: 'user.issues.summary.configurationInvalid',
};

const STATE_LABEL_KEYS: Record<IssueState, string> = {
  current: 'user.issues.state.current',
  closed: 'user.issues.state.closed',
};

const RESOURCE_LABEL_KEYS: Record<Issue['resource_kind'], string> = {
  endpoint: 'user.issues.resources.endpoint',
  endpoint_key: 'user.issues.resources.endpoint_key',
  model: 'user.issues.resourceKind.model',
};

export function IssuesPage() {
  const { t } = useTranslation();
  const [searchParams, setSearchParams] = useSearchState();
  const state: IssueState = searchParams.get(ISSUE_STATE_PARAM) === 'closed' ? 'closed' : 'current';
  const session = useUserAuthority();
  const accountID = session.data?.id;
  const scopeReady = Boolean(accountID) && !session.error;
  const pager = useUrlPagePager({
    station: 'user',
    listType: ISSUES_LIST_TYPE,
    scopeKey: accountID ?? 'anonymous',
    scopeReady,
    resetKey: state,
  });
  const selectState = (nextState: IssueState) => {
    setSearchParams((previous) => {
      const next = new URLSearchParams(previous);
      next.set(ISSUE_STATE_PARAM, nextState);
      next.delete('page');
      next.set('page', '1');
      next.delete('page_size');
      next.set('page_size', String(pager.pageSize));
      return next;
    });
  };
  const issues = useIssuePage(
    accountID,
    state,
    pager.page,
    pager.pageSize,
    !session.isPending && !session.isFetching && !session.error,
  );
  const pageData = issues.data;
  const busy = issues.isFetching;

  return (
    <div className="page ops-stack">
      <RecordsHeader
        description={t('user.issues.authorityDescription')}
        issueCount={
          state === 'current' &&
          pageData &&
          !issues.isPlaceholderData &&
          !issues.error &&
          !pageData.projection_incomplete
            ? pageData.pagination.total_items
            : undefined
        }
      />
      <Tabs
        label={t('user.issues.stateLabel')}
        value={state}
        onChange={selectState}
        tabs={[
          { value: 'current', label: t('user.issues.currentTab') },
          { value: 'closed', label: t('user.issues.closedTab') },
        ]}
      />
      {session.error ? (
        <ErrorState error={session.error} onRetry={() => void session.refetch()} />
      ) : session.isPending || (issues.isPending && !pageData) ? (
        <LoadingState />
      ) : issues.error && !pageData ? (
        <ErrorState error={issues.error} onRetry={() => void issues.refetch()} />
      ) : pageData ? (
        <div aria-busy={busy}>
          {issues.error ? (
            <ErrorState error={issues.error} onRetry={() => void issues.refetch()} />
          ) : null}
          {pageData.projection_incomplete ? (
            <p className="inline-notice" role="status">
              {t('user.issues.projectionIncomplete')}
            </p>
          ) : null}
          {pageData.data.length === 0 ? (
            <EmptyState
              title={t('user.issues.empty')}
              body={
                state === 'current'
                  ? t('user.issues.emptyCurrentBody')
                  : t('user.issues.emptyClosedBody')
              }
            />
          ) : (
            <Card>
              {pageData.data.map((issue) => (
                <div key={issue.id} className="nb-row records-issue">
                  <div className="records-issue__heading">
                    <h2>{t(SUMMARY_LABEL_KEYS[issue.summary_code])}</h2>
                    <StatusBadge
                      active={issue.state === 'current'}
                      label={t(STATE_LABEL_KEYS[issue.state])}
                      danger={issue.state === 'current'}
                    />
                  </div>
                  {issue.safe_detail ? <p>{issue.safe_detail}</p> : null}
                  <dl className="nb-facts nb-facts--inline">
                    <dt>{t('user.issues.resourceType')}</dt>
                    <dd>{t(RESOURCE_LABEL_KEYS[issue.resource_kind])}</dd>
                    <dt>{t('user.issues.firstSeen')}</dt>
                    <dd>{formatDateTime(issue.first_seen_at)}</dd>
                    <dt>{t('user.issues.lastSeen')}</dt>
                    <dd>{formatDateTime(issue.last_seen_at)}</dd>
                    <dt>{t('user.issues.observations')}</dt>
                    <dd className="mono">{issue.count}</dd>
                    {issue.closed_at !== null ? (
                      <>
                        <dt>{t('user.issues.closedAt')}</dt>
                        <dd>{formatDateTime(issue.closed_at)}</dd>
                      </>
                    ) : null}
                  </dl>
                  {issue.deep_link ? (
                    <Link
                      className="nb-btn nb-btn--secondary"
                      to={
                        issue.deep_link.route_id === 'endpoint-detail'
                          ? `/endpoints/${encodeURIComponent(issue.deep_link.resource_id)}`
                          : '/models'
                      }
                    >
                      {t('user.issues.openResource')}
                    </Link>
                  ) : (
                    <p className="table-note">
                      {t(
                        issue.state === 'closed'
                          ? 'user.issues.closedResourceUnavailable'
                          : 'user.issues.resourceUnavailable',
                      )}
                    </p>
                  )}
                </div>
              ))}
            </Card>
          )}
          <PagePagination
            metadata={pageData.pagination}
            busy={busy}
            onPageChange={pager.setPage}
            onPageSizeChange={pager.setPageSize}
            requestedPage={pager.page}
          />
        </div>
      ) : (
        <LoadingState />
      )}
    </div>
  );
}
