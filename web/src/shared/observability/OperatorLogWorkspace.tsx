import { lazy, Suspense } from 'react';
import { useTranslation } from 'react-i18next';
import { LoadingState } from '@shared/components/States';
import { Tabs } from '@shared/components/ui/Tabs';
import { useSearchState } from '@shared/operations/useSearchState';
import { LazyIndependentDiagnostics } from './LazyIndependentDiagnostics';

const Requests = lazy(() =>
  import('@shared/components/log/RoleLogPanel').then(({ RoleLogPanel }) => ({
    default: RoleLogPanel,
  })),
);

interface Props {
  role: 'admin' | 'steward';
  accountId?: string;
  scopeReady: boolean;
  canReadRequests: boolean;
  canReadActivity: boolean;
  onAuthorityLoss?: () => void;
}

export function OperatorLogWorkspace({
  role,
  accountId,
  scopeReady,
  canReadRequests,
  canReadActivity,
  onAuthorityLoss,
}: Props) {
  const { t, i18n } = useTranslation();
  const [params, setParams] = useSearchState();
  if (!scopeReady || !accountId || (!canReadRequests && !canReadActivity)) return null;
  const activity = canReadActivity && (!canReadRequests || params.get('log_view') === 'activity');
  const content = (
    <Suspense fallback={<LoadingState />}>
      {activity ? (
        <LazyIndependentDiagnostics role={role} accountId={accountId} scopeReady />
      ) : (
        <Requests
          role={role}
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
