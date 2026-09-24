import { useLocation } from 'react-router';
import { UserManagement } from '@shared/components/UserManagement';
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
import { MaintenancePanel } from '@shared/operations/MaintenancePanel';
import { operationsKeys, useUserAuthority } from '../features/operations/data';
import '@shared/operations/operations.css';

export function StewardPage() {
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
        requestedTab === 'risk' ||
        requestedTab === 'announcements'
      ? requestedTab
      : 'logs';
  const announcement = searchParams.get('announcement') ?? '';
  const setSection = useCallback(
    (value: 'logs' | 'charity' | 'maintenance' | 'users' | 'announcements' | 'risk') => {
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
    <div className="page ops-page">
      <PageHeader
        title={t('user.steward.title')}
        description={t('user.steward.operationsDescription')}
      />
      {!trainee ? (
        <div className="ops-tabs" role="tablist" aria-label={t('user.steward.sectionsLabel')}>
          <button
            className={section === 'risk' ? 'btn btn-primary' : 'btn btn-secondary'}
            type="button"
            role="tab"
            aria-selected={section === 'risk'}
            onClick={() => setSection('risk')}
          >
            {t('common.audit.risk')}
          </button>
          <button
            className={section === 'logs' ? 'btn btn-primary' : 'btn btn-secondary'}
            type="button"
            role="tab"
            aria-selected={section === 'logs'}
            onClick={() => setSection('logs')}
          >
            {t('user.steward.logsTab')}
          </button>
          <button
            className={section === 'charity' ? 'btn btn-primary' : 'btn btn-secondary'}
            type="button"
            role="tab"
            aria-selected={section === 'charity'}
            onClick={() => setSection('charity')}
          >
            {t('user.steward.charityTab')}
          </button>
          {(['users', 'announcements'] as const).map((tab) => (
            <button
              key={tab}
              className={section === tab ? 'btn btn-primary' : 'btn btn-secondary'}
              type="button"
              role="tab"
              aria-selected={section === tab}
              onClick={() => setSection(tab)}
            >
              {tab === 'users' ? t('user.steward.usersTab') : t('user.steward.announcementsTab')}
            </button>
          ))}
          <button
            className={section === 'maintenance' ? 'btn btn-danger' : 'btn btn-secondary'}
            type="button"
            role="tab"
            aria-selected={section === 'maintenance'}
            onClick={() => setSection('maintenance')}
          >
            {t('user.steward.maintenanceTab')}
          </button>
        </div>
      ) : null}
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
        <MaintenancePanel
          key={`maintenance:${authority.data.id}`}
          role="steward"
          onAuthorityLoss={authorityLoss}
        />
      ) : null}
    </div>
  );
}
