import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { useLocation } from 'react-router';
import { useUserAuthority } from '../features/operations/data';
import { useIssuePage } from '../features/operations/issuePage';
import { PageHeader } from '@shared/components/States';
import { DEFAULT_PAGE_SIZE } from '@shared/operations/usePagePager';
import { SectionTabs } from './SectionTabs';
import './records.css';

export function RecordsHeader({
  description,
  actions,
  issueCount,
}: {
  description?: string;
  actions?: ReactNode;
  issueCount?: string;
}) {
  const { t } = useTranslation();
  const location = useLocation();
  const session = useUserAuthority();
  const onCurrentIssues =
    location.pathname === '/issues' &&
    new URLSearchParams(location.search).get('state') !== 'closed';
  const current = useIssuePage(
    session.data?.id,
    'current',
    '1',
    DEFAULT_PAGE_SIZE,
    !onCurrentIssues && !session.isFetching && !session.error,
  );
  const count =
    issueCount ??
    (!onCurrentIssues &&
    !session.error &&
    !session.isFetching &&
    !current.error &&
    !current.isPlaceholderData &&
    !current.data?.projection_incomplete
      ? current.data?.pagination.total_items
      : undefined);
  return (
    <div className="records-header">
      <PageHeader
        icon="diagnostics"
        title={t('user.records.title')}
        description={description}
        actions={actions}
      />
      <SectionTabs
        group="records"
        className="records-tabs"
        badges={{
          '/issues':
            count !== undefined && count !== '0' ? (
              <span className="nb-badge nb-badge--warn nb-badge--plain">{count}</span>
            ) : undefined,
        }}
      />
    </div>
  );
}
