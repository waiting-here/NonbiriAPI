import { useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { useQueryClient } from '@tanstack/react-query';
import { clearStationSession } from '@shared/charityManagement';
import { CharityManagement } from '@shared/components/CharityManagement';
import { PageHeader } from '@shared/components/States';
import { AdminCharityGroupsPanel } from '../features/operations/AdminCharityGroups';
import '@shared/operations/operations.css';

export function CharityPage() {
  const { t } = useTranslation();
  const client = useQueryClient();
  const clearAuthority = useCallback(() => {
    clearStationSession(client, 'admin');
  }, [client]);
  return (
    <div className="page ops-page">
      <PageHeader title={t('admin.charity.title')} description={t('admin.charity.description')} />
      <CharityManagement frame="admin" onCapabilityLoss={clearAuthority} />
      <AdminCharityGroupsPanel />
    </div>
  );
}
