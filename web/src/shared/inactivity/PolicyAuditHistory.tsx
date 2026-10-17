import { useDateTimeFormatter } from '@shared/utils/datetime';
import { getAudits } from './api';
import { PolicyHistory } from './PolicyHistory';

export function PolicyAuditHistory({ zh, locale }: { zh: boolean; locale?: string }) {
  const formatDateTime = useDateTimeFormatter();
  return (
    <PolicyHistory
      kind="audits"
      title={zh ? '配置与预览审计' : 'Configuration and preview audit'}
      zh={zh}
      load={getAudits}
    >
      {(rows) => (
        <ul>
          {rows.map((item) => (
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
      )}
    </PolicyHistory>
  );
}
