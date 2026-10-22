import { useTranslation } from 'react-i18next';
import { Note } from '@shared/components/ui';
import { CoreErrorPanel } from '../components';
import type { OperationOutcome } from '@shared/operations/useRetainedOperation';
import type { ResourceStatus } from '../resourceOperation';

interface ConnectionOperation {
  error: unknown;
  refreshError: unknown;
  outcome: OperationOutcome;
  isPending: boolean;
  check: () => Promise<void>;
  refresh?: () => Promise<void>;
  reset: () => void;
  receipt?: ResourceStatus | null;
}
export function ConnectionOperationNotice({ operation }: { operation: ConnectionOperation }) {
  const { t } = useTranslation();
  return (
    <>
      {operation.error || operation.refreshError ? (
        <CoreErrorPanel
          error={operation.error ?? operation.refreshError}
          onRetry={
            operation.outcome === 'refresh-failed' && operation.refresh
              ? () => void operation.refresh?.().catch(() => undefined)
              : undefined
          }
        />
      ) : null}
      {operation.outcome === 'unknown' ? (
        <Note tone="warn">
          <p>{t('common.connections.unknownResult')}</p>
          <div className="nb-actions">
            <button
              type="button"
              className="nb-btn nb-btn--secondary"
              disabled={operation.isPending}
              onClick={() => void operation.check().catch(() => undefined)}
            >
              {t('common.connections.checkResult')}
            </button>
            {operation.receipt?.status === 'not_recorded' ? (
              <button type="button" className="nb-btn nb-btn--secondary" onClick={operation.reset}>
                {t('common.connections.resumeUnrecorded')}
              </button>
            ) : null}
          </div>
        </Note>
      ) : null}
      {operation.outcome === 'confirmed' ? (
        <Note tone="ok">{t('common.connections.saved')}</Note>
      ) : null}
    </>
  );
}
