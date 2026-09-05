import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQueryClient } from '@tanstack/react-query';
import { useSearchParams } from 'react-router';
import { clearStationSession } from '@shared/charityManagement';
import { CharityManagement } from '@shared/components/CharityManagement';
import { RoleLogPanel } from '@shared/components/log';
import { Card, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { MaintenancePanel } from '@shared/operations/MaintenancePanel';
import { operationsKeys, useUserAuthority } from '../features/operations/data';
import '@shared/operations/operations.css';

export function StewardPage() {
  const { t } = useTranslation();
  const client = useQueryClient();
  const authority = useUserAuthority();
  const refetchAuthority = authority.refetch;
  const [searchParams, setSearchParams] = useSearchParams();
  const section =
    searchParams.get('tab') === 'charity'
      ? 'charity'
      : searchParams.get('tab') === 'maintenance'
        ? 'maintenance'
        : 'logs';
  const setSection = useCallback(
    (value: 'logs' | 'charity' | 'maintenance') => {
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
    authority.data && authority.data.effective_level === 5 && !authority.data.is_banned,
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
      <div className="ops-tabs" role="tablist" aria-label={t('user.steward.sectionsLabel')}>
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
      {section === 'logs' ? (
        <RoleLogPanel
          key={`logs:${authority.dataUpdatedAt}`}
          role="steward"
          enabled
          onAuthorityLoss={authorityLoss}
        />
      ) : null}
      {section === 'charity' ? (
        <CharityManagement
          key={`charity:${authority.dataUpdatedAt}`}
          frame="steward"
          onCapabilityLoss={authorityLoss}
        />
      ) : null}
      {section === 'maintenance' ? (
        <MaintenancePanel
          key={`maintenance:${authority.dataUpdatedAt}`}
          role="steward"
          onAuthorityLoss={authorityLoss}
        />
      ) : null}
    </div>
  );
}
