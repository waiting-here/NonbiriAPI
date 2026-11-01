import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { PageHeader } from '@shared/components/States';
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
            issueCount !== undefined && issueCount !== '0' ? (
              <span className="nb-badge nb-badge--warn nb-badge--plain">{issueCount}</span>
            ) : undefined,
        }}
      />
    </div>
  );
}
