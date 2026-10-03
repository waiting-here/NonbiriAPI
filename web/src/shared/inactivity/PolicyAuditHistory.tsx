import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { ErrorState } from '@shared/components/States';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { getAudits } from './api';

export function PolicyAuditHistory({ zh, locale }: { zh: boolean; locale?: string }) {
  const formatDateTime = useDateTimeFormatter();
  const [enabled, setEnabled] = useState(false);
  const [cursor, setCursor] = useState<string>();
  const query = useQuery({
    queryKey: ['inactivity-policy-audits', cursor],
    queryFn: ({ signal }) => getAudits(cursor, signal),
    enabled,
  });
  return (
    <details className="nb-fold" onToggle={(event) => setEnabled(event.currentTarget.open)}>
      <summary>
        <span className="nb-fold__title">
          <strong>{zh ? '配置与预览审计' : 'Configuration and preview audit'}</strong>
        </span>
      </summary>
      <div className="nb-fold__body">
        {enabled && query.isPending && <p role="status">{zh ? '正在读取…' : 'Loading…'}</p>}
        {query.isError && <ErrorState error={query.error} onRetry={() => void query.refetch()} />}
        {query.data && (
          <>
            <ul>
              {query.data.data.map((item) => (
                <li key={item.id}>
                  <details>
                    <summary>
                      {formatDateTime(item.created_at, locale === 'zh' ? 'zh' : 'en')} ·{' '}
                      {item.action === 'configure'
                        ? zh
                          ? '配置变更'
                          : 'Policy change'
                        : zh
                          ? '预览'
                          : 'Preview'}{' '}
                      · {zh ? '配置版本' : 'Revision'} {item.policy_revision} ·{' '}
                      {item.actor_user_id ?? (zh ? '已去标识' : 'Deidentified')}
                    </summary>
                    <pre>{JSON.stringify(item.details, null, 2)}</pre>
                  </details>
                </li>
              ))}
            </ul>
            {query.data.next_cursor && (
              <button
                className="nb-btn nb-btn--secondary"
                onClick={() => setCursor(query.data.next_cursor ?? undefined)}
              >
                {zh ? '下一页审计' : 'Next audit page'}
              </button>
            )}
          </>
        )}
      </div>
    </details>
  );
}
