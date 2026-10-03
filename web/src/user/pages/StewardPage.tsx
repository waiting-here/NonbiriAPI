import { useLocation } from 'react-router';
import { UserManagement } from '@shared/components/UserManagement';
import { BlacklistManagement } from '../../admin/pages/BlacklistPage';
import { AnnouncementManagement } from '@shared/components/AnnouncementManagement';
import { AnnouncementEditor } from '@shared/components/AnnouncementEditor';
import { listReturnPath } from '@shared/operations/listReturn';
import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQueryClient } from '@tanstack/react-query';
import { useSearchState } from '@shared/operations/useSearchState';
import { clearStationSession } from '@shared/charityManagement';
import { CharityManagement } from '@shared/components/CharityManagement';
import { RoleLogPanel } from '@shared/components/log';
import { IndependentDiagnostics } from '@shared/observability/IndependentDiagnostics';
import { RiskAuditPanel } from '@shared/riskAudit/Panel';
import { Card, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { Tabs, Panel } from '@shared/components/ui';
import { MaintenancePanel } from '@shared/operations/MaintenancePanel';
import { TimeContextProvider } from '@shared/components/TimeContext';
import { operationsKeys, useUserAuthority } from '../features/operations/data';
import '@shared/operations/operations.css';

const sectionKeys = {
  risk: 'common.audit.risk',
  logs: 'user.steward.logsTab',
  charity: 'user.steward.charityTab',
  users: 'user.steward.usersTab',
  blacklist: 'user.steward.blacklistTab',
  announcements: 'user.steward.announcementsTab',
  maintenance: 'user.steward.maintenanceTab',
} as const;

export function StewardPage() {
  return <StewardPageContent />;
}

function StewardPageContent() {
  const { t } = useTranslation();
  const client = useQueryClient();
  const authority = useUserAuthority();
  const refetchAuthority = authority.refetch;
  const [searchParams, setSearchParams] = useSearchState();
  const location = useLocation();
  const trainee = authority.data?.effective_level === 5;
  const requestedTab = searchParams.get('tab');
  const section = trainee
    ? 'charity'
    : requestedTab === 'charity' ||
        requestedTab === 'maintenance' ||
        requestedTab === 'users' ||
        requestedTab === 'blacklist' ||
        requestedTab === 'risk' ||
        requestedTab === 'announcements'
      ? requestedTab
      : 'logs';
  const announcement = searchParams.get('announcement') ?? '';
  const setSection = useCallback(
    (
      value: 'logs' | 'charity' | 'maintenance' | 'users' | 'blacklist' | 'announcements' | 'risk',
    ) => {
      setSearchParams({ tab: value }, { replace: true });
    },
    [setSearchParams],
  );
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
  }, [clearSensitiveQueries, refetchAuthority, setSection]);

  const allowed = Boolean(
    authority.data && [5, 6].includes(authority.data.effective_level) && !authority.data.is_banned,
  );
  useEffect(() => {
    if (!authority.isPending && !allowed) clearSensitiveQueries();
  }, [allowed, authority.isPending, clearSensitiveQueries]);

  if (authority.isPending || authorityRefreshing) return <LoadingState />;
  if (authority.error)
    return (
      <div className="page ops-page">
        <PageHeader title={t('user.steward.title')} description={t('user.steward.description')} />
        <ErrorState error={authority.error} onRetry={() => void authority.refetch()} />
      </div>
    );
  if (!allowed)
    return (
      <div className="page ops-page">
        <PageHeader title={t('user.steward.title')} description={t('user.steward.description')} />
        <Card>
          <p className="field-error" role="alert">
            {t('user.steward.accessDenied')} {t('user.steward.sensitiveStateCleared')}
          </p>
        </Card>
      </div>
    );

  return (
    <TimeContextProvider station="steward">
      <div className="page ops-page">
        {section !== 'users' && section !== 'blacklist' ? (
          <PageHeader
            title={t('user.steward.title')}
            description={t('user.steward.presentation.description')}
          />
        ) : null}
        <Tabs
          label={t('user.steward.sectionsLabel')}
          value={section}
          onChange={setSection}
          tabs={(trainee
            ? (['charity'] as const)
            : ([
                'risk',
                'logs',
                'charity',
                'users',
                'blacklist',
                'announcements',
                'maintenance',
              ] as const)
          ).map((value) => ({
            value,
            label: t(sectionKeys[value]),
          }))}
        />
        {!trainee && section === 'logs' ? (
          <>
            <RoleLogPanel
              key={`logs:${authority.data.id}`}
              role="steward"
              accountId={authority.data.id}
              scopeReady={allowed}
              enabled
              onAuthorityLoss={authorityLoss}
            />
            <IndependentDiagnostics
              role="steward"
              accountId={authority.data.id}
              scopeReady={allowed}
              enabled
            />
          </>
        ) : null}
        {section === 'risk' ? (
          <RiskAuditPanel role="steward" scopeKey={authority.data.id} enabled={allowed} />
        ) : null}
        {section === 'charity' ? (
          <CharityManagement
            key={`charity:${authority.data.id}`}
            frame="steward"
            trainee={trainee}
            accountId={authority.data.id}
            onCapabilityLoss={authorityLoss}
          />
        ) : null}
        {section === 'users' ? (
          <UserManagement
            key={`users:${authority.data.id}`}
            role="steward"
            account={authority.data.id}
            scopeReady={allowed}
            sessionError={authority.error}
            onAuthorityLoss={authorityLoss}
          />
        ) : null}
        {section === 'blacklist' ? (
          <BlacklistManagement
            key={`blacklist:${authority.data.id}`}
            role="steward"
            accountID={`steward:${authority.data.id}`}
            sessionError={authority.error}
            sessionFetching={authority.isFetching}
            refreshSession={() => void authority.refetch()}
            onAuthorityLoss={authorityLoss}
          />
        ) : null}
        {section === 'announcements' ? (
          announcement ? (
            <AnnouncementEditor
              key={`announcement:${authority.data.id}:${announcement}`}
              role="steward"
              account={authority.data.id}
              announcementId={announcement}
              backTo={
                listReturnPath(location.state, '/steward') === '/steward'
                  ? '/steward?tab=announcements'
                  : listReturnPath(location.state, '/steward')
              }
              onAuthorityLoss={authorityLoss}
            />
          ) : (
            <AnnouncementManagement
              key={`announcements:${authority.data.id}`}
              role="steward"
              account={authority.data.id}
              onAuthorityLoss={authorityLoss}
            />
          )
        ) : null}
        {section === 'maintenance' ? (
          <Panel tone="danger">
            <MaintenancePanel
              key={`maintenance:${authority.data.id}`}
              role="steward"
              onAuthorityLoss={authorityLoss}
            />
          </Panel>
        ) : null}
      </div>
    </TimeContextProvider>
  );
}
