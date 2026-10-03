import { useId } from 'react';
import { useTranslation } from 'react-i18next';
import { transportRules, type TransportRule } from '@shared/transportRule';

const optionKeys = {
  passthrough: 'common.transportRule.options.passthrough',
  force_non_stream: 'common.transportRule.options.force_non_stream',
  force_stream: 'common.transportRule.options.force_stream',
} as const;
const helpKeys = {
  passthrough: 'common.transportRule.help.passthrough',
  force_non_stream: 'common.transportRule.help.force_non_stream',
  force_stream: 'common.transportRule.help.force_stream',
} as const;

export function TransportRuleField({
  value,
  onChange,
  disabled,
}: {
  value: TransportRule;
  onChange: (value: TransportRule) => void;
  disabled?: boolean;
}) {
  const { t } = useTranslation();
  const id = useId();
  return (
    <label>
      <span>{t('common.transportRule.label')}</span>
      <select
        value={value}
        disabled={disabled}
        aria-label={t('common.transportRule.label')}
        aria-describedby={`${id}-help`}
        onChange={(event) => onChange(event.target.value as TransportRule)}
      >
        {transportRules.map((rule) => (
          <option key={rule} value={rule}>
            {t(optionKeys[rule])}
          </option>
        ))}
      </select>
      <small id={`${id}-help`}>{t(helpKeys[value])}</small>
    </label>
  );
}

export function TransportRuleSummary({ value }: { value: TransportRule }) {
  const { t } = useTranslation();
  return (
    <div>
      <dt>{t('common.transportRule.label')}</dt>
      <dd>{t(optionKeys[value])}</dd>
    </div>
  );
}
