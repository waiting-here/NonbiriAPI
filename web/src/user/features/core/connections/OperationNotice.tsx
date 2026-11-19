import { useTranslation } from 'react-i18next';
import { Link } from 'react-router';
import { useRef, useState } from 'react';
import { Field, Note } from '@shared/components/ui';
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
  needsSecret?: boolean;
  retryOriginal?: (secret?: string) => Promise<void>;
}
function RetryOriginal({ operation }: { operation: ConnectionOperation }) {
  const { t } = useTranslation();
  const secret = useRef<HTMLInputElement>(null);
  const [checking, setChecking] = useState(false);
  return (
    <>
      {operation.needsSecret ? (
        <Field label={t('user.quickstart.secretAgain')}>
          {(props) => (
            <input {...props} type="password" autoComplete="off" ref={secret} disabled={checking} />
          )}
        </Field>
      ) : null}
      <button
        type="button"
        className="nb-btn nb-btn--secondary"
        disabled={checking || operation.isPending}
        onClick={() => {
          if (operation.needsSecret && !secret.current?.value) {
            secret.current?.focus();
            return;
          }
          const value = secret.current?.value;
          if (secret.current) secret.current.value = '';
          setChecking(true);
          void operation
            .retryOriginal?.(value)
            .catch(() => undefined)
            .finally(() => setChecking(false));
        }}
      >
        {t('common.connections.resumeUnrecorded')}
      </button>
    </>
  );
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
            {operation.receipt?.status === 'not_recorded' && operation.retryOriginal ? (
              <RetryOriginal operation={operation} />
            ) : null}
            {operation.receipt?.status === 'expired' ? (
              <div className="nb-stack nb-stack--sm">
                <p>{t('user.quickstart.expired')}</p>
                <Link to="/endpoints">{t('user.quickstart.useExistingRecovery')}</Link>
              </div>
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
