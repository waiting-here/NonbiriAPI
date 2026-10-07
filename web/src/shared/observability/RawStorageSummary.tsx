import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { apiFetch } from '@shared/query/http';
import { Fold } from '@shared/components/ui/Fold';
import type { DiagnosticRole } from './api';
import { IndependentDiagnostics } from './IndependentDiagnostics';

interface Capacity {
  budget_bytes: number;
  used_bytes: number;
  capacity_omissions: number;
  unavailable: number;
  current_missing: number;
}

export function RawStorageSummary({ role }: { role: DiagnosticRole }) {
  const { t } = useTranslation();
  const [value, setValue] = useState<Capacity>();
  const [showRecords, setShowRecords] = useState(false);
  useEffect(() => {
    const controller = new AbortController();
    const root = role === 'admin' ? '/admin/api' : '/api/steward';
    apiFetch<Capacity>(`${root}/logs/diagnostic-capacity`, { signal: controller.signal })
      .then(setValue)
      .catch(() => {
        /* Ordinary logs remain usable when diagnostics are unavailable. */
      });
    return () => controller.abort();
  }, [role]);
  if (!value) return null;
  return (
    <div className={value.current_missing ? 'log-storage log-storage--warn' : 'log-storage'}>
      <Fold
        title={t('common.operations.logs.presentation.storage')}
        summary={t('common.operations.logs.presentation.storageSummary', {
          used: (value.used_bytes / 1_048_576).toFixed(2),
          budget: (value.budget_bytes / 1_048_576).toFixed(0),
          count: value.capacity_omissions + value.unavailable,
        })}
      >
        <small>
          {t('common.operations.logs.presentation.storageFailures', {
            count: value.unavailable,
            capacity: value.capacity_omissions,
            current: value.current_missing ?? 0,
          })}
        </small>
        <p>{t('common.operations.logs.presentation.storageHistoryHelp')}</p>
        <button
          className="btn btn-secondary"
          type="button"
          aria-expanded={showRecords}
          onClick={() => setShowRecords(!showRecords)}
        >
          {t('common.operations.logs.presentation.storageRecords')}
        </button>
        {showRecords ? (
          <IndependentDiagnostics role={role} scopeReady accountId="storage" storage />
        ) : null}
      </Fold>
    </div>
  );
}
