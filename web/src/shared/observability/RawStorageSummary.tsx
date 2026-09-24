import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { apiFetch } from '@shared/query/http';
import type { DiagnosticRole } from './api';

interface Capacity {
  budget_bytes: number;
  used_bytes: number;
  capacity_omissions: number;
  unavailable: number;
}

export function RawStorageSummary({ role }: { role: DiagnosticRole }) {
  const { i18n } = useTranslation();
  const zh = i18n.resolvedLanguage?.startsWith('zh');
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
    <small>
      {zh ? '错误正文载荷' : 'Error body payload'}: {(value.used_bytes / 1_048_576).toFixed(2)} /{' '}
      {(value.budget_bytes / 1_048_576).toFixed(0)} MiB ·{' '}
      {zh ? '容量不足未保存' : 'Omitted for capacity'} {value.capacity_omissions} ·{' '}
      {zh ? '读取或保存失败' : 'Read or storage failures'} {value.unavailable}.{' '}
      {zh ? '此容量不包含数据库页、索引和 WAL。' : 'This excludes database pages, indices and WAL.'}
    </small>
  );
}
