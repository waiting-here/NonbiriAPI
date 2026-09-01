import { useCallback } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { clearStationSession } from '@shared/charityManagement';
import { CharityManagement } from '@shared/components/CharityManagement';
import { PageHeader } from '@shared/components/States';
import '@shared/operations/operations.css';

export function CharityPage() {
  const client = useQueryClient();
  const clearAuthority = useCallback(() => {
    clearStationSession(client, 'admin');
  }, [client]);
  return <div className="page ops-page"><PageHeader title="Charity management" description="Review donor submissions, manage safe key limits, and publish logical charity routing without exposing secrets." /><CharityManagement frame="admin" onCapabilityLoss={clearAuthority} /></div>;
}
