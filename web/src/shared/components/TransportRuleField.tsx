import { useId } from 'react';
import { Segmented } from './ui';
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
    <div className="nb-field">
      <span className="nb-field__label">{t('common.transportRule.label')}</span>
      <Segmented
        label={t('common.transportRule.label')}
        value={value}
        disabled={disabled}
        describedBy={`${id}-help`}
        onChange={onChange}
        options={transportRules.map((rule) => ({ value: rule, label: t(optionKeys[rule]) }))}
      />
      <small id={`${id}-help`} className="nb-field__help">
        {t(helpKeys[value])}
      </small>
    </div>
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
