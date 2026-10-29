import { Link, useLocation } from 'react-router';
import { UserManagement } from '@shared/components/UserManagement';
import { BlacklistManagement } from '../../admin/pages/BlacklistPage';
import { AnnouncementManagement } from '@shared/components/AnnouncementManagement';
import { AnnouncementEditor } from '@shared/components/AnnouncementEditor';
import { listReturnPath } from '@shared/operations/listReturn';
import { useCallback, useEffect, useRef, useState } from 'react';
import {
  hasStewardPermission,
  canAccessStewardCharityManagement,
  type StewardPermission,
} from '@shared/operations/stewardPermissions';
import { StewardPermissionNotice } from '@shared/components/StewardPermissionNotice';
import { useTranslation } from 'react-i18next';
import { useQueryClient } from '@tanstack/react-query';
import { useSearchState } from '@shared/operations/useSearchState';
import { CharityManagement } from '@shared/components/CharityManagement';
import { RoleLogPanel } from '@shared/components/log';
import { IndependentDiagnostics } from '@shared/observability/IndependentDiagnostics';
import { RiskAuditPanel } from '@shared/riskAudit/Panel';
import { Card, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { Tabs, Panel } from '@shared/components/ui';
import { MaintenancePanel } from '@shared/operations/MaintenancePanel';
import { TimeContextProvider } from '@shared/components/TimeContext';
import { useUserAuthority } from '../features/operations/data';
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
  const requestedTab = searchParams.get('tab');
  const section =
    requestedTab === 'charity' ||
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
    const predicate = (query: { queryKey: readonly unknown[] }) =>
      (query.queryKey[0] === 'charity-management' && query.queryKey[1] === 'steward') ||
      (query.queryKey[0] === 'risk' && query.queryKey[1] === 'steward') ||
      (query.queryKey[0] === 'steward' && query.queryKey[1] === 'global-pressure') ||
      (query.queryKey[0] === 'user' &&
        (query.queryKey[1] === 'steward' ||
          query.queryKey[1] === 'steward-logs' ||
          query.queryKey[1] === 'operations'));
    void client.cancelQueries({ predicate });
    client.removeQueries({ predicate });
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
  const has = (permission: StewardPermission) => hasStewardPermission(authority.data, permission);
  const revision = authority.data?.steward_permissions?.revision;
  const previousRevision = useRef(revision);
  useEffect(() => {
    if (previousRevision.current !== revision) clearSensitiveQueries();
    previousRevision.current = revision;
  }, [revision, clearSensitiveQueries]);
  const sectionAllowed =
    section === 'charity'
      ? canAccessStewardCharityManagement(authority.data)
      : section === 'logs'
        ? has('logs.read') || has('diagnostics.read')
        : has(
            (
              {
                risk: 'risk.read',
                users: 'users.read',
                blacklist: 'blacklist.read',
                announcements: 'announcements.read',
                maintenance: 'maintenance.read',
              } as const
            )[section],
          );
  useEffect(() => {
    if (!authority.isPending && !allowed) clearSensitiveQueries();
  }, [allowed, authority.isPending, clearSensitiveQueries]);

  if (authority.isPending || authorityRefreshing) return <LoadingState />;
  if (authority.error)
    return (
      <div className="page ops-page">
        <PageHeader
          icon="steward"
          title={t('user.steward.title')}
          description={t('user.steward.description')}
        />
        <ErrorState error={authority.error} onRetry={() => void authority.refetch()} />
      </div>
    );
  if (!authority.data || !allowed)
    return (
      <div className="page ops-page">
        <PageHeader
          icon="steward"
          title={t('user.steward.title')}
          description={t('user.steward.description')}
        />
        <Card>
          <p className="field-error" role="alert">
            {t('user.steward.accessDenied')} {t('user.steward.sensitiveStateCleared')}
          </p>
        </Card>
      </div>
    );

  return (
    <TimeContextProvider
      key={`${authority.data.id}:${authority.data.effective_level}:${revision ?? '0'}`}
      station="steward"
    >
      <div className="page ops-page">
        <PageHeader
          icon="steward"
          title={t('user.steward.title')}
          description={t('user.steward.presentation.description')}
        />
        <div className="nb-actions">
          <Link className="nb-btn nb-btn--secondary" to="/steward/ai">
            {t('common.agent.title')}
          </Link>
          <Link
            className="nb-btn nb-btn--secondary"
            to="/steward/endpoints"
            state={{ returnTo: location.pathname + location.search }}
          >
            {t('management.endpoints.title')}
          </Link>
          <Link
            className="nb-btn nb-btn--secondary"
            to="/endpoint-policies"
            state={{ returnTo: location.pathname + location.search }}
          >
            {t('common.endpointPolicies.title')}
          </Link>
        </div>
        <Tabs
          label={t('user.steward.sectionsLabel')}
          value={section}
          onChange={setSection}
          tabs={(
            [
              'risk',
              'logs',
              'charity',
              'users',
              'blacklist',
              'announcements',
              'maintenance',
            ] as const
          ).map((value) => ({
            value,
            label: t(sectionKeys[value]),
          }))}
        />
        {!sectionAllowed ? <StewardPermissionNotice /> : null}
        {sectionAllowed && section === 'logs' ? (
          <>
            {has('logs.read') ? (
              <RoleLogPanel
                key={`logs:${authority.data.id}:${authority.data.effective_level}`}
                role="steward"
                accountId={`${authority.data.id}:${authority.data.effective_level}`}
                scopeReady={allowed}
                enabled
                onAuthorityLoss={authorityLoss}
              />
            ) : (
              <StewardPermissionNotice />
            )}
            {has('diagnostics.read') ? (
              <IndependentDiagnostics
                role="steward"
                accountId={`${authority.data.id}:${authority.data.effective_level}`}
                scopeReady={allowed}
                enabled
              />
            ) : (
              <StewardPermissionNotice />
            )}
          </>
        ) : null}
        {sectionAllowed && section === 'risk' ? (
          <RiskAuditPanel role="steward" scopeKey={authority.data.id} enabled={allowed} embedded />
        ) : null}
        {sectionAllowed && section === 'charity' ? (
          <CharityManagement
            key={`charity:${authority.data.id}`}
            frame="steward"
            accountId={authority.data.id}
            actorLevel={authority.data.effective_level === 5 ? 5 : 6}
            permissions={authority.data.steward_permissions}
            onCapabilityLoss={authorityLoss}
          />
        ) : null}
        {sectionAllowed && section === 'users' ? (
          <UserManagement
            key={`users:${authority.data.id}:${authority.data.effective_level}`}
            role="steward"
            actorLevel={authority.data.effective_level === 5 ? 5 : 6}
            permissions={authority.data.steward_permissions}
            account={authority.data.id}
            scopeReady={allowed}
            sessionError={authority.error}
            onAuthorityLoss={authorityLoss}
            embedded
          />
        ) : null}
        {sectionAllowed && section === 'blacklist' ? (
          <BlacklistManagement
            key={`blacklist:${authority.data.id}`}
            role="steward"
            accountID={`steward:${authority.data.id}`}
            sessionError={authority.error}
            sessionFetching={authority.isFetching}
            refreshSession={() => void authority.refetch()}
            onAuthorityLoss={authorityLoss}
            embedded
          />
        ) : null}
        {sectionAllowed && section === 'announcements' ? (
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
              embedded
            />
          )
        ) : null}
        {sectionAllowed && section === 'maintenance' ? (
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
