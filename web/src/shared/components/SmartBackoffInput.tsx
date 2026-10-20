import { useTranslation } from 'react-i18next';
import {
  SMART_BACKOFF_PROFILES,
  SMART_BACKOFF_PERIODS,
  smartBackoffPeriods,
  validSmartBackoff,
  type SmartBackoffConfiguration,
  type SmartBackoffProfile,
  type SmartBackoffRule,
} from '@shared/operations/smartBackoff';
import { Button } from './ui/Button';
import { Field } from './ui/Field';
import { Fold } from './ui/Fold';
import './FailurePolicyControl.css';

const profileKeys = {
  none: 'common.smartBackoff.profiles.none',
  five_hour_week: 'common.smartBackoff.profiles.five_hour_week',
  five_hour_week_month: 'common.smartBackoff.profiles.five_hour_week_month',
  day: 'common.smartBackoff.profiles.day',
  day_month: 'common.smartBackoff.profiles.day_month',
} as const;
const periodKeys = {
  auto: 'common.smartBackoff.periods.auto',
  five_hour: 'common.smartBackoff.periods.five_hour',
  day: 'common.smartBackoff.periods.day',
  week: 'common.smartBackoff.periods.week',
  month: 'common.smartBackoff.periods.month',
} as const;

export function SmartBackoffInput({
  value,
  onChange,
  disabled,
}: {
  value: SmartBackoffConfiguration;
  onChange: (value: SmartBackoffConfiguration) => void;
  disabled?: boolean;
}) {
  const { t } = useTranslation();
  const profile = value.smart_backoff_profile;
  const rules = value.smart_backoff_rules;
  const updateRule = (index: number, patch: Partial<SmartBackoffRule>) =>
    onChange({
      ...value,
      smart_backoff_rules: rules.map((rule, position) =>
        position === index ? { ...rule, ...patch } : rule,
      ),
    });
  const allowed = smartBackoffPeriods(profile);
  return (
    <div className="smart-backoff-input" onClick={(event) => event.stopPropagation()}>
      <Field label={t('common.smartBackoff.profile')} help={t('common.smartBackoff.help')}>
        {(props) => (
          <select
            {...props}
            value={profile}
            disabled={disabled}
            onChange={(event) =>
              onChange({
                ...value,
                smart_backoff_profile: event.target.value as SmartBackoffProfile,
              })
            }
          >
            {SMART_BACKOFF_PROFILES.map((item) => (
              <option key={item} value={item}>
                {t(profileKeys[item])}
              </option>
            ))}
          </select>
        )}
      </Field>
      <Fold
        plain
        title={t('common.smartBackoff.advanced')}
        summary={t(
          rules.length === 0
            ? 'common.smartBackoff.ruleCount_zero'
            : rules.length === 1
              ? 'common.smartBackoff.ruleCount_one'
              : 'common.smartBackoff.ruleCount_other',
          { count: rules.length },
        )}
      >
        <p className="muted">{t('common.smartBackoff.rulesHelp')}</p>
        {profile === 'none' && rules.length > 0 ? (
          <p className="muted">{t('common.smartBackoff.rulesInactive')}</p>
        ) : null}
        <div className="smart-backoff-rules">
          {rules.map((rule, index) => (
            <div className="smart-backoff-rule" key={index}>
              <div className="smart-backoff-rule-heading">
                <h4>{t('common.smartBackoff.ruleHeading', { index: index + 1 })}</h4>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  disabled={disabled}
                  onClick={() =>
                    onChange({
                      ...value,
                      smart_backoff_rules: rules.filter((_, position) => position !== index),
                    })
                  }
                >
                  {t('common.smartBackoff.removeRule', { index: index + 1 })}
                </Button>
              </div>
              <Field label={t('common.smartBackoff.kindLabel')}>
                {(props) => (
                  <select
                    {...props}
                    disabled={disabled}
                    value={rule.kind}
                    aria-label={t('common.smartBackoff.ruleKind', { index: index + 1 })}
                    onChange={(event) =>
                      updateRule(index, { kind: event.target.value as SmartBackoffRule['kind'] })
                    }
                  >
                    <option value="code">{t('common.smartBackoff.kinds.code')}</option>
                    <option value="message">{t('common.smartBackoff.kinds.message')}</option>
                  </select>
                )}
              </Field>
              <Field label={t('common.smartBackoff.valueLabel')}>
                {(props) => (
                  <input
                    {...props}
                    disabled={disabled}
                    value={rule.value}
                    aria-label={t('common.smartBackoff.ruleValue', { index: index + 1 })}
                    maxLength={rule.kind === 'code' ? 64 : 320}
                    placeholder={t(
                      rule.kind === 'code'
                        ? 'common.smartBackoff.placeholder.code'
                        : 'common.smartBackoff.placeholder.message',
                    )}
                    onChange={(event) => updateRule(index, { value: event.target.value })}
                  />
                )}
              </Field>
              <Field label={t('common.smartBackoff.periodLabel')}>
                {(props) => (
                  <select
                    {...props}
                    value={rule.period}
                    aria-label={t('common.smartBackoff.rulePeriod', { index: index + 1 })}
                    disabled={disabled}
                    onChange={(event) =>
                      updateRule(index, {
                        period: event.target.value as SmartBackoffRule['period'],
                      })
                    }
                  >
                    {SMART_BACKOFF_PERIODS.map((period) => (
                      <option key={period} value={period} disabled={!allowed.includes(period)}>
                        {t(periodKeys[period])}
                      </option>
                    ))}
                  </select>
                )}
              </Field>
            </div>
          ))}
        </div>
        <Button
          type="button"
          disabled={disabled || rules.length >= 8}
          onClick={() =>
            onChange({
              ...value,
              smart_backoff_rules: [...rules, { kind: 'code', value: '', period: 'auto' }],
            })
          }
        >
          {t('common.smartBackoff.addRule')}
        </Button>
      </Fold>
      <Fold plain title={t('common.smartBackoff.details')}>
        <p className="muted">{t('common.smartBackoff.timingHelp')}</p>
        <p className="muted">{t('common.smartBackoff.editingHelp')}</p>
        <p className="muted">{t('common.smartBackoff.recognitionHelp')}</p>
        <p className="muted">{t('common.smartBackoff.valueHelp.code')}</p>
        <p className="muted">{t('common.smartBackoff.valueHelp.message')}</p>
        <p className="muted">{t('common.smartBackoff.successHelp')}</p>
      </Fold>
      {!validSmartBackoff(value) ? (
        <p className="field-error" role="alert">
          {t('common.smartBackoff.invalid')}
        </p>
      ) : null}
    </div>
  );
}
