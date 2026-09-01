import { useCallback, useEffect, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { clearStationSession } from '@shared/charityManagement';
import { CharityManagement } from '@shared/components/CharityManagement';
import { RoleLogPanel } from '@shared/components/log';
import { Card, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { MaintenancePanel } from '@shared/operations/MaintenancePanel';
import { operationsKeys, useUserAuthority } from '../features/operations/data';
import '@shared/operations/operations.css';

export function StewardPage() {
  const client = useQueryClient();
  const authority = useUserAuthority();
  const refetchAuthority = authority.refetch;
  const [section, setSection] = useState<'logs' | 'charity' | 'maintenance'>('logs');
  const [authorityRefreshing, setAuthorityRefreshing] = useState(false);
  const clearSensitiveQueries = useCallback(() => {
    clearStationSession(client, 'steward');
    client.setQueryData(operationsKeys.session, null);
  }, [client]);
  const authorityLoss = useCallback(() => {
    setAuthorityRefreshing(true);
    clearSensitiveQueries();
    setSection('logs');
    void refetchAuthority().finally(() => setAuthorityRefreshing(false));
  }, [clearSensitiveQueries, refetchAuthority]);

  const allowed = Boolean(authority.data && authority.data.effective_level === 5 && !authority.data.is_banned);
  useEffect(() => { if (!authority.isPending && !allowed) clearSensitiveQueries(); }, [allowed, authority.isPending, clearSensitiveQueries]);

  if (authority.isPending || authorityRefreshing) return <LoadingState />;
  if (authority.error) return <div className="page ops-page"><PageHeader title="Steward" description="Real-time level-five operations." /><ErrorState error={authority.error} onRetry={() => void authority.refetch()} /></div>;
  if (!allowed) return <div className="page ops-page"><PageHeader title="Steward" description="Real-time level-five operations." /><Card><p className="field-error" role="alert">This page requires a current, unbanned account whose effective level is exactly 5. Sensitive steward state has been cleared.</p></Card></div>;

  return (
    <div className="page ops-page">
      <PageHeader title="Steward" description="Admin-safe logs, owner-narrowed charity management, and the one-way maintenance emergency surface." />
      <div className="ops-tabs" role="tablist" aria-label="Steward sections"><button className={section === 'logs' ? 'btn btn-primary' : 'btn btn-secondary'} type="button" role="tab" aria-selected={section === 'logs'} onClick={() => setSection('logs')}>Safe logs</button><button className={section === 'charity' ? 'btn btn-primary' : 'btn btn-secondary'} type="button" role="tab" aria-selected={section === 'charity'} onClick={() => setSection('charity')}>My charity resources</button><button className={section === 'maintenance' ? 'btn btn-danger' : 'btn btn-secondary'} type="button" role="tab" aria-selected={section === 'maintenance'} onClick={() => setSection('maintenance')}>Maintenance</button></div>
      {section === 'logs' ? <RoleLogPanel key={`logs:${authority.dataUpdatedAt}`} role="steward" enabled onAuthorityLoss={authorityLoss} /> : null}
      {section === 'charity' ? <CharityManagement key={`charity:${authority.dataUpdatedAt}`} frame="steward" onCapabilityLoss={authorityLoss} /> : null}
      {section === 'maintenance' ? <MaintenancePanel key={`maintenance:${authority.dataUpdatedAt}`} role="steward" onAuthorityLoss={authorityLoss} /> : null}
    </div>
  );
}
