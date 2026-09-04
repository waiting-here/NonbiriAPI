import { useState } from 'react';
import { Link } from 'react-router';
import { useTranslation } from 'react-i18next';
import { Card, EmptyState, ErrorState, LoadingState, PageHeader, StatusBadge } from '@shared/components/States';
import { CursorPagination } from '@shared/operations/CursorPagination';
import { useCursorPager } from '@shared/operations/useCursorPager';
import { formatDateTime } from '@shared/utils/datetime';
import { useIssues, type Issue } from '../features/operations/data';
import '@shared/operations/operations.css';

const SUMMARY_LABEL_KEYS: Record<Issue['summary_code'], string> = {
  discovery_failed: 'user.issues.summary.discoveryFailed',
  no_routable_binding: 'user.issues.summary.noRoutableBinding',
  credential_invalid: 'user.issues.summary.credentialInvalid',
  configuration_invalid: 'user.issues.summary.configurationInvalid',
};

const STATE_LABEL_KEYS: Record<Issue['state'], string> = {
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
  const [state, setState] = useState<'current' | 'closed'>('current');
  const pager = useCursorPager();
  const issues = useIssues(state, pager.cursor);
  const switchState = (next: 'current' | 'closed') => { setState(next); pager.reset(); };
  return <div className="page ops-stack">
    <PageHeader eyebrow={t('user.issues.eyebrow')} title={t('user.issues.title')}
      description={t('user.issues.authorityDescription')} />
    <div className="ops-tabs" role="tablist" aria-label={t('user.issues.stateLabel')}>
      <button type="button" role="tab" aria-selected={state === 'current'} className={`btn ${state === 'current' ? 'btn-primary' : 'btn-secondary'}`} onClick={() => switchState('current')}>{t('user.issues.currentTab')}</button>
      <button type="button" role="tab" aria-selected={state === 'closed'} className={`btn ${state === 'closed' ? 'btn-primary' : 'btn-secondary'}`} onClick={() => switchState('closed')}>{t('user.issues.closedTab')}</button>
    </div>
    {issues.isPending ? <LoadingState /> : issues.error ? <ErrorState error={issues.error} onRetry={() => void issues.refetch()} /> : <>
      {issues.data.projection_incomplete ? <p className="inline-notice" role="status">{t('user.issues.projectionIncomplete')}</p> : null}
      {issues.data.data.length === 0 ? <EmptyState title={t('user.issues.empty')} body={state === 'current' ? t('user.issues.emptyCurrentBody') : t('user.issues.emptyClosedBody')} /> : <div className="ops-stack">
        {issues.data.data.map((issue) => <Card key={issue.id} className="ops-stack">
          <div className="card-title-row"><h2>{t(SUMMARY_LABEL_KEYS[issue.summary_code])}</h2>
            <StatusBadge active={issue.state === 'current'} label={t(STATE_LABEL_KEYS[issue.state])} danger={issue.state === 'current'} /></div>
          {issue.safe_detail ? <p>{issue.safe_detail}</p> : null}
          <dl className="ops-kv">
            <dt>{t('user.issues.resource')}</dt><dd>{t(RESOURCE_LABEL_KEYS[issue.resource_kind])}</dd>
            <dt>{t('user.issues.firstSeen')}</dt><dd>{formatDateTime(issue.first_seen_at)}</dd>
            <dt>{t('user.issues.lastSeen')}</dt><dd>{formatDateTime(issue.last_seen_at)}</dd>
            <dt>{t('user.issues.observations')}</dt><dd className="mono">{issue.count}</dd>
            {issue.closed_at !== null ? <><dt>{t('user.issues.closedAt')}</dt><dd>{formatDateTime(issue.closed_at)}</dd></> : null}
          </dl>
          {issue.deep_link ? <Link className="btn btn-secondary" to={issue.deep_link.route_id === 'endpoint-detail' ? `/endpoints/${encodeURIComponent(issue.deep_link.resource_id)}` : '/models'}>{t('user.issues.openResource')}</Link> : <p className="table-note">{t('user.issues.resourceUnavailable')}</p>}
        </Card>)}
      </div>}
      <CursorPagination page={pager.page} nextCursor={issues.data.next_cursor} onPrevious={pager.previous} onNext={pager.next}
        labels={{ previous: t('user.issues.previous'), next: t('user.issues.next'), page: t('user.issues.page') }} />
    </>}
  </div>;
}
