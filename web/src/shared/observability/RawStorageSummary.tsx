import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { apiFetch } from '@shared/query/http';
import { Fold } from '@shared/components/ui/Fold';
import type { DiagnosticRole } from './api';

interface Capacity {
  budget_bytes: number;
  used_bytes: number;
  capacity_omissions: number;
  unavailable: number;
}

export function RawStorageSummary({ role }: { role: DiagnosticRole }) {
  const { t } = useTranslation();
  const [value, setValue] = useState<Capacity>();
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
    <div
      className={
        value.capacity_omissions || value.unavailable
          ? 'log-storage log-storage--warn'
          : 'log-storage'
      }
    >
      <Fold
        title={t('common.operations.logs.presentation.storage')}
        summary={t('common.operations.logs.presentation.storageSummary', {
          used: (value.used_bytes / 1_048_576).toFixed(2),
          budget: (value.budget_bytes / 1_048_576).toFixed(0),
          count: value.capacity_omissions + value.unavailable,
        })}
      >
        <small>
          {t('common.operations.logs.presentation.storageFailures', { count: value.unavailable })}
        </small>
      </Fold>
    </div>
  );
}
