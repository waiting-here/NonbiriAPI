import { useTranslation } from 'react-i18next';
import { CopyValue } from '@shared/components/CopyValue';
import type { CallerIdentity as CallerIdentityValue } from './data';

interface CallerIdentityProps {
  identity: CallerIdentityValue | null;
}

export function CallerIdentity({ identity }: CallerIdentityProps) {
  const { t } = useTranslation();
  const unavailable = t('logs.callerUnavailable');
  if (identity === null) {
    return (
      <span className="log-caller-identity">
        <span className="log-caller-identity__value">{unavailable}</span>
      </span>
    );
  }
  const { discord_nickname: nickname, discord_id: discordID } = identity;

  return (
    <span className="log-caller-identity">
      <span className="log-caller-identity__line">
        <span className="log-caller-identity__label">{t('logs.callerNickname')}</span>
        <span className="log-caller-identity__value">{nickname ?? unavailable}</span>
      </span>
      <span className="log-caller-identity__line">
        <span className="log-caller-identity__label">{t('logs.callerDiscordId')}</span>
        {discordID === null ? (
          <span className="log-caller-identity__value">{unavailable}</span>
        ) : (
          <CopyValue value={discordID} label={t('logs.callerDiscordId')} />
        )}
      </span>
    </span>
  );
}
