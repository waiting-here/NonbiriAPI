import { INFERENCE_LIMITS, type InferenceLimitDraft } from '@shared/operations/userInferenceLimits';
import { Fragment } from 'react';
import { useTranslation } from 'react-i18next';
import type { AdminUser } from '@shared/operations/managedUsers';
import { formatCount } from '@shared/utils/formatNumber';
import { Field } from './ui';

export function InferenceLimitFields({
  user,
  value,
  onChange,
}: {
  user: AdminUser;
  value: InferenceLimitDraft;
  onChange: (value: InferenceLimitDraft) => void;
}) {
  const { t } = useTranslation();
  return (
    <div className="nb-grid nb-grid--2">
      {INFERENCE_LIMITS.map(({ key, label }) => (
        <Field
          key={key}
          label={t(label)}
          help={t(
            user[key] === null
              ? 'management.users.limitInherited'
              : 'management.users.limitEffective',
            { value: formatCount(user[('effective_' + key) as keyof AdminUser] as string).display },
          )}
        >
          {(props) => (
            <input
              {...props}
              inputMode="numeric"
              placeholder={t('management.users.inheritLimit')}
              value={value[key]}
              onChange={(event) => onChange({ ...value, [key]: event.target.value })}
            />
          )}
        </Field>
      ))}
    </div>
  );
}
export function InferenceLimitSummary({ user }: { user: AdminUser }) {
  const { t } = useTranslation();
  return (
    <dl className="nb-kv">
      {INFERENCE_LIMITS.map(({ key, label }) => (
        <Fragment key={key}>
          <dt>{t(label)}</dt>
          <dd>
            <span className="nb-num">
              {formatCount(user[('effective_' + key) as keyof AdminUser] as string).display}
            </span>
            {user[key] === null ? ' · ' + t('management.users.inheritLimit') : ''}
          </dd>
        </Fragment>
      ))}
    </dl>
  );
}
