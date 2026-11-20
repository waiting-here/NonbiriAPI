import { useQueryClient } from '@tanstack/react-query';
import type { DiagnosticQuery } from './IndependentDiagnostics';
import { Suspense } from 'react';
import { useTranslation } from 'react-i18next';
import { RoleLogPanel } from '@shared/components/log/RoleLogPanel';
import { LoadingState } from '@shared/components/States';
import { Tabs } from '@shared/components/ui/Tabs';
import { useSearchState } from '@shared/operations/useSearchState';
import { LazyIndependentDiagnostics } from './LazyIndependentDiagnostics';

interface Props {
  role: 'admin' | 'steward';
  accountId?: string;
  scopeReady: boolean;
  canReadModels?: boolean;
  canReadUsers?: boolean;
  canReadRequests: boolean;
  canReadActivity: boolean;
  onAuthorityLoss?: () => void;
}

export function OperatorLogWorkspace({
  role,
  accountId,
  scopeReady,
  canReadModels,
  canReadUsers,
  canReadRequests,
  canReadActivity,
  onAuthorityLoss,
}: Props) {
  const { t, i18n } = useTranslation();
  const client = useQueryClient();
  const activityKey = [
    role === 'admin' ? 'admin' : 'user',
    'operations',
    role,
    'activity-position',
    accountId,
  ];

  const [params, setParams] = useSearchState();
  if (!scopeReady || !accountId || (!canReadRequests && !canReadActivity)) return null;
  const activity = canReadActivity && (!canReadRequests || params.get('log_view') === 'activity');
  const content = (
    <Suspense fallback={<LoadingState />}>
      {activity ? (
        <LazyIndependentDiagnostics
          role={role}
          accountId={accountId}
          scopeReady
          initialQuery={client.getQueryData<DiagnosticQuery>(activityKey)}
          remember={(query) =>
            query
              ? client.setQueryData(activityKey, query)
              : client.removeQueries({ queryKey: activityKey })
          }
        />
      ) : (
        <RoleLogPanel
          role={role}
          canReadModels={canReadModels}
          canReadUsers={canReadUsers}
          accountId={accountId}
          scopeReady
          language={i18n.resolvedLanguage}
          onAuthorityLoss={onAuthorityLoss}
        />
      )}
    </Suspense>
  );
  if (!canReadRequests || !canReadActivity) return content;
  return (
    <Tabs
      label={t('logs.workspaceLabel')}
      value={activity ? 'activity' : 'requests'}
      onChange={(next) =>
        setParams((current) => {
          if (next === 'activity') current.set('log_view', 'activity');
          else current.delete('log_view');
          return current;
        })
      }
      tabs={[
        { value: 'requests', label: t('logs.workspaceRequests') },
        { value: 'activity', label: t('logs.workspaceActivity') },
      ]}
    >
      {content}
    </Tabs>
  );
}
