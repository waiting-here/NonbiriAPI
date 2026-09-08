import { Link, useSearchParams } from 'react-router';
import { useTranslation } from 'react-i18next';
import {
  Card,
  EmptyState,
  ErrorState,
  LoadingState,
  PageHeader,
  StatusBadge,
} from '@shared/components/States';
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
  endpoint: 'user.issues.resourceKind.endpoint',
  endpoint_key: 'user.issues.resourceKind.endpointKey',
  model: 'user.issues.resourceKind.model',
};

export function IssuesPage() {
  const { t } = useTranslation();
  const [searchParams, setSearchParams] = useSearchParams();
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
      <PageHeader
        eyebrow={t('user.issues.eyebrow')}
        title={t('user.issues.title')}
        description={t('user.issues.authorityDescription')}
      />
      <div className="ops-tabs" role="tablist" aria-label={t('user.issues.stateLabel')}>
        <button
          type="button"
          role="tab"
          aria-selected={state === 'current'}
          className={`btn ${state === 'current' ? 'btn-primary' : 'btn-secondary'}`}
          onClick={() => selectState('current')}
        >
          {t('user.issues.currentTab')}
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={state === 'closed'}
          className={`btn ${state === 'closed' ? 'btn-primary' : 'btn-secondary'}`}
          onClick={() => selectState('closed')}
        >
          {t('user.issues.closedTab')}
        </button>
      </div>
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
            <div className="ops-stack">
              {pageData.data.map((issue) => (
                <Card key={issue.id} className="ops-stack">
                  <div className="card-title-row">
                    <h2>{t(SUMMARY_LABEL_KEYS[issue.summary_code])}</h2>
                    <StatusBadge
                      active={issue.state === 'current'}
                      label={t(STATE_LABEL_KEYS[issue.state])}
                      danger={issue.state === 'current'}
                    />
                  </div>
                  {issue.safe_detail ? <p>{issue.safe_detail}</p> : null}
                  <dl className="ops-kv">
                    <dt>{t('user.issues.resource')}</dt>
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
                      className="btn btn-secondary"
                      to={
                        issue.deep_link.route_id === 'endpoint-detail'
                          ? `/endpoints/${encodeURIComponent(issue.deep_link.resource_id)}`
                          : '/models'
                      }
                    >
                      {t('user.issues.openResource')}
                    </Link>
                  ) : (
                    <p className="table-note">{t('user.issues.resourceUnavailable')}</p>
                  )}
                </Card>
              ))}
            </div>
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
