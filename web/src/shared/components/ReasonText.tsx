import { useTranslation } from 'react-i18next';
import type { AutomaticReason } from '@shared/operations/automaticReason';
import { accountHistoryCopy } from './accountCopy';

export function ReasonText({
  reason,
  automatic,
  reasonCode,
  reasonCodes,
}: {
  reason: string;
  automatic?: AutomaticReason | null;
  reasonCode?: string;
  reasonCodes?: string[];
}) {
  const { t } = useTranslation();
  const history = accountHistoryCopy(t);
  const labels =
    reasonCodes?.flatMap((code) =>
      code === 'deletion_penalty_evasion' || code === 'deletion_debt_evasion'
        ? [history.reasons[code]]
        : [],
    ) ?? [];
  if (labels.length)
    return (
      <div className="ops-stack">
        <ul>
          {labels.map((label) => (
            <li key={label}>{label}</li>
          ))}
        </ul>
        {reason ? <p className="ops-break ops-blacklist-note">{reason}</p> : null}
      </div>
    );
  if (automatic?.kind === 'client_rules' && automatic.schema_version === 1) {
    return (
      <div className="ops-stack">
        <p>{t('common.reasons.clientRules')}</p>
        <ul>
          {automatic.rules.map((rule, index) => (
            <li key={index} className="ops-break">
              {rule.name}
            </li>
          ))}
        </ul>
        {automatic.manual_text ? (
          <p className="ops-break ops-blacklist-note">
            {t('common.reasons.existingManual')}: {automatic.manual_text}
          </p>
        ) : null}
      </div>
    );
  }
  const code =
    reasonCode === 'charity_rpm'
      ? 'common.reasons.charityRPM'
      : reasonCode === 'charity_short_content'
        ? 'common.reasons.charityShortContent'
        : undefined;
  return (
    <p className={code ? 'ops-break' : 'ops-break ops-blacklist-note'}>
      {code ? t(code) : automatic?.manual_text || reason || t('common.reasons.unspecified')}
    </p>
  );
}
