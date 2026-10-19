import { useTranslation } from 'react-i18next';
import { KEY_STATES, type KeyStateFilter as KeyState } from '@shared/operations/keyState';
import { Field } from '../ui';

const stateKeys = {
  available: 'common.operations.charity.charityState.available',
  disabled: 'common.operations.charity.charityState.disabled',
  suspended: 'common.operations.charity.charityState.suspended',
  exhausted: 'common.operations.charity.charityState.exhausted',
  rate_limited: 'common.operations.charity.charityState.rate_limited',
  pending: 'common.operations.charity.charityState.pending',
  expired: 'common.operations.charity.charityState.expired',
  ended: 'common.operations.charity.charityState.ended',
  all: 'common.all',
} as const;

export function KeyStateFilter({
  value,
  onChange,
  disabled,
}: {
  value: KeyState;
  onChange: (value: KeyState) => void;
  disabled?: boolean;
}) {
  const { t } = useTranslation();
  return (
    <Field label={t('common.workflow.keyState')}>
      {(props) => (
        <select
          {...props}
          value={value}
          disabled={disabled}
          onChange={(event) => onChange(event.target.value as KeyState)}
        >
          {KEY_STATES.map((state) => (
            <option value={state} key={state}>
              {t(stateKeys[state])}
            </option>
          ))}
        </select>
      )}
    </Field>
  );
}
