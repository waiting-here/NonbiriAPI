import { useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { useQueryClient } from '@tanstack/react-query';
import { clearStationSession } from '@shared/charityManagement';
import { CharityManagement } from '@shared/components/CharityManagement';
import { PageHeader } from '@shared/components/States';
import { AdminCharityGroupsPanel } from '../features/operations/AdminCharityGroups';
import { useAdminSession } from '../data';
import '@shared/operations/operations.css';

export function CharityPage() {
  const { t } = useTranslation();
  const client = useQueryClient();
  const session = useAdminSession();
  const accountId = session.data ? `admin:${session.data.admin.username}` : undefined;
  const clearAuthority = useCallback(() => {
    clearStationSession(client, 'admin');
  }, [client]);
  return (
    <div className="page ops-page">
      <PageHeader title={t('admin.charity.title')} description={t('admin.charity.description')} />
      <CharityManagement
        key={accountId}
        frame="admin"
        accountId={accountId}
        onCapabilityLoss={clearAuthority}
        sourceGroups={<AdminCharityGroupsPanel />}
      />
    </div>
  );
}
