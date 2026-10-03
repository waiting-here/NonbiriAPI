import { Link } from 'react-router';
import { useTranslation } from 'react-i18next';
import type { LogOrigin } from '@shared/operations/logOrigin';

export function LogOriginIdentity({
  value,
  role,
  compact = false,
  nickname,
}: {
  value: LogOrigin;
  role: 'admin' | 'steward';
  compact?: boolean;
  nickname?: string | null;
}) {
  const { t } = useTranslation();
  const params = new URLSearchParams();
  if (role === 'steward') params.set('tab', 'users');
  if (value.origin_deleted && value.history_record_id)
    params.set('deleted', value.history_record_id);
  else if (!value.origin_deleted && value.origin_user_id) params.set('user', value.origin_user_id);
  const available = Boolean(value.origin_deleted ? value.history_record_id : value.origin_user_id);
  if (compact)
    return (
      <span className="log-origin-compact">
        <span>
          {nickname ||
            (value.origin_user_id ? `#${value.origin_user_id}` : t('common.history.unknown'))}
          {value.origin_deleted ? (
            <span className="nb-badge nb-badge--plain">{t('common.history.deleted')}</span>
          ) : null}
        </span>
        <span className="nb-sub">
          {nickname && value.origin_user_id ? `#${value.origin_user_id} · ` : ''}
          {value.origin_discord_id ? (
            <span title={value.origin_discord_id}>
              {value.origin_discord_id.length > 10
                ? `${value.origin_discord_id.slice(0, 4)}…${value.origin_discord_id.slice(-4)}`
                : value.origin_discord_id}
            </span>
          ) : (
            t('common.history.identityPartial')
          )}
          {available && value.origin_deleted ? (
            <>
              {' '}
              ·{' '}
              <Link to={`${role === 'admin' ? '/users' : '/steward'}?${params.toString()}`}>
                {t('common.history.openHistory')}
              </Link>
            </>
          ) : null}
        </span>
      </span>
    );
  return (
    <span className="ops-stack">
      <span>
        {t('common.history.originalAccount')}: {value.origin_user_id ?? t('common.history.unknown')}
      </span>
      <span>
        {t('common.history.discord')}: {value.origin_discord_id ?? t('common.history.unknown')}
      </span>
      {value.origin_deleted ? (
        <span className="status-badge">{t('common.history.deleted')}</span>
      ) : null}
      {value.origin_unknown ? (
        <span className="muted">{t('common.history.identityPartial')}</span>
      ) : null}
      {available ? (
        <Link to={`${role === 'admin' ? '/users' : '/steward'}?${params.toString()}`}>
          {t(value.origin_deleted ? 'common.history.openHistory' : 'common.history.openAccount')}
        </Link>
      ) : null}
    </span>
  );
}
