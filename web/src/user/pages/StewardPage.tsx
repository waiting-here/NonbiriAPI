import { Link, useLocation } from 'react-router';
import { decimalAmount } from '@shared/inactivity/amounts';
import { logManagementReturnPath } from '@shared/components/log/managementNavigation';
import { listReturnPath } from '@shared/operations/listReturn';
import { lazy, Suspense, useCallback, useEffect, useRef, useState } from 'react';
import {
  hasStewardPermission,
  defaultStewardPermissions,
  stewardLimitKeys,
  canAccessStewardCharityManagement,
  type StewardPermission,
} from '@shared/operations/stewardPermissions';
import { permissionGroups } from '@shared/operations/stewardPermissionGroups';
import { permissionPresentationCopy } from '@shared/components/permissionPresentationCopy';
import { StewardPermissionNotice } from '@shared/components/StewardPermissionNotice';
import { useTranslation } from 'react-i18next';
import { useQueryClient } from '@tanstack/react-query';
import { useSearchState } from '@shared/operations/useSearchState';
import { Card, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { Tabs } from '@shared/components/ui';
import { TimeContextProvider } from '@shared/components/TimeContext';
import { useUserAuthority } from '../features/operations/data';
import '@shared/operations/operations.css';

const OperatorLogWorkspace = lazy(() =>
  import('@shared/observability/OperatorLogWorkspace').then(({ OperatorLogWorkspace }) => ({
    default: OperatorLogWorkspace,
  })),
);
const UserManagement = lazy(() =>
  import('@shared/components/UserManagement').then((module) => ({
    default: module.UserManagement,
  })),
);
const BlacklistManagement = lazy(() =>
  import('../../admin/pages/BlacklistPage').then((module) => ({
    default: module.BlacklistManagement,
  })),
);
const AnnouncementManagement = lazy(() =>
  import('@shared/components/AnnouncementManagement').then((module) => ({
    default: module.AnnouncementManagement,
  })),
);
const AnnouncementEditor = lazy(() =>
  import('@shared/components/AnnouncementEditor').then((module) => ({
    default: module.AnnouncementEditor,
  })),
);
const CharityManagement = lazy(() =>
  import('@shared/components/CharityManagement').then((module) => ({
    default: module.CharityManagement,
  })),
);
const RiskAuditPanel = lazy(() =>
  import('@shared/riskAudit/Panel').then((module) => ({ default: module.RiskAuditPanel })),
);
const MaintenancePanel = lazy(() =>
  import('@shared/operations/MaintenancePanel').then((module) => ({
    default: module.MaintenancePanel,
  })),
);

const sectionKeys = {
  risk: 'common.audit.risk',
  logs: 'user.steward.logsTab',
  charity: 'user.steward.charityTab',
  users: 'user.steward.usersTab',
  blacklist: 'user.steward.blacklistTab',
  announcements: 'user.steward.announcementsTab',
  maintenance: 'user.steward.maintenanceTab',
} as const;

const sections = [
  'logs',
  'risk',
  'charity',
  'users',
  'blacklist',
  'announcements',
  'maintenance',
] as const;
type Section = (typeof sections)[number];

export function StewardPage() {
  const { t } = useTranslation();
  const client = useQueryClient();
  const authority = useUserAuthority();
  const refetchAuthority = authority.refetch;
  const [searchParams, setSearchParams] = useSearchState();
  const location = useLocation();
  const requestedTab = searchParams.get('tab');
  const has = (permission: StewardPermission) => hasStewardPermission(authority.data, permission);
  const access: Record<Section, boolean> = {
    logs: has('logs.read') || has('diagnostics.read'),
    risk: has('risk.read') || has('risk.configure'),
    charity: canAccessStewardCharityManagement(authority.data),
    users: has('users.read'),
    blacklist: has('blacklist.read'),
    announcements: has('announcements.read'),
    maintenance: has('maintenance.read'),
  };
  const section =
    sections.find((value) => value === requestedTab) ??
    sections.find((value) => access[value]) ??
    'logs';
  const logReturnTo = logManagementReturnPath(location.state, 'steward', location.search);
  const announcement = searchParams.get('announcement') ?? '';
  const tabQueries = useRef<Partial<Record<Section, string>>>({});
  function setSection(value: Section) {
    tabQueries.current[section] = searchParams.toString();
    const next = new URLSearchParams(tabQueries.current[value]);
    next.set('tab', value);
    setSearchParams(next, { state: null });
  }
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
    const cancelled = client.cancelQueries({ predicate });
    client.removeQueries({ predicate });
    return cancelled;
  }, [client]);
  const authorityLoss = useCallback(() => {
    tabQueries.current = {};
    setAuthorityRefreshing(true);
    void clearSensitiveQueries();
    setSearchParams({}, { replace: true });
    void refetchAuthority().finally(() => setAuthorityRefreshing(false));
  }, [clearSensitiveQueries, refetchAuthority, setSearchParams]);

  const allowed = Boolean(
    authority.data && [5, 6].includes(authority.data.effective_level) && !authority.data.is_banned,
  );
  const revision = authority.data?.steward_permissions?.revision;
  const authorityScope = authority.data
    ? `${authority.data.id}:${authority.data.effective_level}:${revision ?? '0'}`
    : '';
  const visibleSection = access[section];
  // Mount sensitive readers only after queries from the previous policy are cleared.
  const [clearedScope, setClearedScope] = useState(authorityScope);
  const scopeChanged = clearedScope !== authorityScope;
  useEffect(() => {
    if (!scopeChanged) return;
    tabQueries.current = {};
    if (
      clearedScope &&
      (clearedScope.split(':')[0] !== authority.data?.id || !allowed || !visibleSection) &&
      (location.search || location.state)
    )
      setSearchParams({}, { replace: true, state: null });
    let active = true;
    void clearSensitiveQueries().then(() => {
      if (active) setClearedScope(authorityScope);
    });
    return () => {
      active = false;
    };
  }, [
    authorityScope,
    authority.data?.id,
    allowed,
    visibleSection,
    scopeChanged,
    clearedScope,
    clearSensitiveQueries,
    setSearchParams,
    location.search,
    location.state,
  ]);
  useEffect(() => {
    if (!authority.isPending && !allowed) void clearSensitiveQueries();
  }, [allowed, authority.isPending, clearSensitiveQueries]);

  const policy =
    authority.data?.steward_permissions ??
    defaultStewardPermissions(authority.data?.effective_level ?? 0);
  const permissionCopy = permissionPresentationCopy(t);
  if (authority.isPending || authorityRefreshing)
    return (
      <div className="page ops-page">
        <LoadingState />
      </div>
    );
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

  if (scopeChanged)
    return (
      <div className="page ops-page">
        <LoadingState />
      </div>
    );
  return (
    <TimeContextProvider key={authorityScope} station="steward">
      <div className="page ops-page">
        <PageHeader
          icon="steward"
          title={t('user.steward.title')}
          description={t('user.steward.presentation.description')}
          actions={
            <div className="nb-actions">
              {(has('ai.chat') || has('ai.review.read')) && (
                <Link
                  className="nb-btn nb-btn--secondary"
                  to="/steward/ai"
                  state={{ returnTo: location.pathname + location.search }}
                >
                  {t('common.agent.title')}
                </Link>
              )}
              {has('endpoints.read') && (
                <Link
                  className="nb-btn nb-btn--secondary"
                  to="/steward/endpoints"
                  state={{ returnTo: location.pathname + location.search }}
                >
                  {t('management.endpoints.title')}
                </Link>
              )}
              {(has('charity.backoff') || has('egress.read')) && (
                <Link
                  className="nb-btn nb-btn--secondary"
                  to="/endpoint-policies"
                  state={{ returnTo: location.pathname + location.search }}
                >
                  {t('common.endpointPolicies.title')}
                </Link>
              )}
            </div>
          }
        />
        <details className="nb-card">
          <summary>{t('common.governance.myPermissions')}</summary>
          <p>
            {t('management.permissions.scope.title')} ·{' '}
            {t(`management.permissions.scope.${policy.donation_scope}.label`)}
          </p>
          {permissionGroups.map((group) => {
            const granted = group.keys.filter((key) => key !== 'ai.memory.edit' && has(key));
            return (
              granted.length > 0 && (
                <p key={group.id}>
                  <strong>{permissionCopy.groups[group.id].title}</strong> ·{' '}
                  {granted
                    .map((key) => t(`management.permissions.labels.${key.replaceAll('.', '_')}`))
                    .join(' · ')}
                </p>
              )
            );
          })}
          <dl>
            {stewardLimitKeys.map((key) => (
              <div key={key}>
                <dt>{t(`management.permissions.limits.${key.replaceAll('.', '_')}`)}</dt>
                <dd>
                  {key.endsWith('_milli')
                    ? decimalAmount(policy.limits[key], 3)
                    : policy.limits[key]}
                </dd>
              </div>
            ))}
          </dl>
        </details>
        {section === 'users' && logReturnTo && (
          <Link to={logReturnTo} state={{logPrivate:location.state?.logPrivate}}>{t('common.operations.logs.returnToLogs')}</Link>
        )}
        <Tabs
          label={t('user.steward.sectionsLabel')}
          value={section}
          onChange={setSection}
          tabs={sections
            .filter((value) => access[value] || value === section)
            .map((value) => ({
              value,
              label: t(sectionKeys[value]),
            }))}
        >
          {access[section] ? (
            <Suspense fallback={<LoadingState />}>
              {section === 'logs' ? (
                <OperatorLogWorkspace
                  role="steward"
                  accountId={authorityScope}
                  scopeReady={allowed}
                  canReadModels={has('charity.read')}
                  canReadUsers={has('users.read')}
                  canReadRequests={has('logs.read')}
                  canReadActivity={has('diagnostics.read')}
                  onAuthorityLoss={authorityLoss}
                />
              ) : null}
              {section === 'risk' ? (
                <RiskAuditPanel
                  role="steward"
                  scopeKey={authority.data.id}
                  enabled={allowed}
                  canRead={has('risk.read')}
                  canConfigure={has('risk.configure')}
                  embedded
                />
              ) : null}
              {section === 'charity' ? (
                <CharityManagement
                  key={`charity:${authority.data.id}`}
                  frame="steward"
                  accountId={authority.data.id}
                  actorLevel={authority.data.effective_level === 5 ? 5 : 6}
                  permissions={authority.data.steward_permissions}
                  onCapabilityLoss={authorityLoss}
                />
              ) : null}
              {section === 'users' ? (
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
              {section === 'blacklist' ? (
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
                    embedded
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
              {section === 'maintenance' ? (
                <MaintenancePanel
                  key={`maintenance:${authority.data.id}`}
                  role="steward"
                  onAuthorityLoss={authorityLoss}
                />
              ) : null}
            </Suspense>
          ) : (
            <>
              <StewardPermissionNotice
                permission={section === 'charity' ? 'charity.read' : section + '.read'}
              />
              {sections.some((value) => access[value]) && (
                <button
                  className="nb-btn nb-btn--secondary"
                  type="button"
                  onClick={() => setSection(sections.find((value) => access[value])!)}
                >
                  {t('common.operations.charity.returnToList')}
                </button>
              )}
            </>
          )}
        </Tabs>
      </div>
    </TimeContextProvider>
  );
}
