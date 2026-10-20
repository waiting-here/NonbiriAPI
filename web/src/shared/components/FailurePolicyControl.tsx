import { Button } from '@shared/components/ui/Button';
import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { charityKeys } from '@shared/operations/charity';
import { responseOutcomeUnknown } from '@shared/operations/api';
import {
  saveFailurePolicy,
  validFailureThreshold,
  type FailurePolicy,
  type FailurePolicyInput,
} from '@shared/operations/failurePolicy';
import {
  smartBackoffInput,
  validSmartBackoff,
  type SmartBackoffProfile,
  type SmartBackoffRule,
} from '@shared/operations/smartBackoff';
import { SmartBackoffInput } from './SmartBackoffInput';
import { Field } from './ui/Field';
import { Note } from './ui/Note';
import { OutcomeNote } from './ui/OutcomeNote';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { ErrorState } from './States';
import { useCharityModelScope } from './charityModelScopeContext';
import './FailurePolicyControl.css';

export function FailurePolicyWarning() {
  const { t } = useTranslation();
  return (
    <Note tone="warn">
      <p className="failure-policy-warning">{t('common.failurePolicy.zeroWarning')}</p>
    </Note>
  );
}
export function FailureThresholdInput({
  value,
  onChange,
  disabled,
}: {
  value: string;
  onChange: (value: string) => void;
  disabled?: boolean;
}) {
  const { t } = useTranslation();
  return (
    <div className="failure-policy-input">
      <Field label={t('common.failurePolicy.label')} help={t('common.failurePolicy.help')}>
        {(props) => (
          <input
            {...props}
            type="text"
            inputMode="numeric"
            maxLength={39}
            value={value}
            disabled={disabled}
            aria-invalid={!validFailureThreshold(value)}
            onClick={(event) => event.stopPropagation()}
            onChange={(event) => onChange(event.target.value)}
          />
        )}
      </Field>
      {value === '0' ? <FailurePolicyWarning /> : null}
    </div>
  );
}

export function FailurePolicyControl({
  role,
  donationID,
  keyID,
  revision,
  threshold,
  profile,
  rules,
  refresh,
}: {
  role: 'owner' | 'admin' | 'steward';
  donationID: string;
  keyID: string;
  revision: string;
  threshold: string;
  profile: SmartBackoffProfile;
  rules: SmartBackoffRule[];
  refresh?: () => Promise<unknown>;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const [draft, setDraft] = useState(threshold);
  const [backoff, setBackoff] = useState({
    smart_backoff_profile: profile,
    smart_backoff_rules: rules,
  });
  const [receipt, setReceipt] = useState<FailurePolicy | null>(null);
  const modelID = useCharityModelScope();
  const root = role === 'owner' ? ['user', 'economy', 'donations'] : charityKeys.root(role);
  const save = useRetainedOperation(
    (input: FailurePolicyInput, key) =>
      saveFailurePolicy(role, donationID, keyID, input, key, modelID),
    () =>
      refresh ? refresh() : client.invalidateQueries({ queryKey: root }, { throwOnError: true }),
    root,
  );
  const unknown = responseOutcomeUnknown(save.error);
  const current =
    receipt && BigInt(receipt.revision) > BigInt(revision)
      ? receipt
      : {
          revision,
          failure_disable_threshold: threshold,
          smart_backoff_profile: profile,
          smart_backoff_rules: rules,
        };
  const valid = validFailureThreshold(draft) && validSmartBackoff(backoff);
  const config = validSmartBackoff(backoff) ? smartBackoffInput(backoff) : backoff;
  const backoffChanged =
    JSON.stringify(config) !==
    JSON.stringify({
      smart_backoff_profile: current.smart_backoff_profile,
      smart_backoff_rules: current.smart_backoff_rules,
    });
  const refreshFailed = save.outcome === 'refresh-failed';
  return (
    <section className="failure-policy-control">
      <p>{t('common.failurePolicy.saved', { value: current.failure_disable_threshold })}</p>
      {current.failure_disable_threshold === '0' && draft !== '0' ? <FailurePolicyWarning /> : null}
      <FailureThresholdInput
        value={draft}
        onChange={(value) => {
          save.reset();
          setDraft(value);
        }}
        disabled={save.isPending || unknown}
      />
      <SmartBackoffInput
        value={backoff}
        disabled={save.isPending || unknown}
        onChange={(value) => {
          save.reset();
          setBackoff(value);
        }}
      />
      <p className="muted">{t('common.failurePolicy.recalculate')}</p>
      <Button
        type="button"
        disabled={
          save.isPending ||
          refreshFailed ||
          (!unknown && (!valid || (draft === current.failure_disable_threshold && !backoffChanged)))
        }
        onClick={() => {
          save.mutate(
            unknown && save.variables
              ? save.variables
              : {
                  expected_revision: current.revision,
                  failure_disable_threshold: draft,
                  ...(backoffChanged ? config : {}),
                },
            { onSuccess: setReceipt },
          );
        }}
      >
        {t(unknown ? 'common.failureReset.resume' : 'common.failurePolicy.save')}
      </Button>
      {save.error ? <ErrorState error={save.error} /> : null}
      {refreshFailed ? (
        <OutcomeNote outcome={{ kind: 'savedRefreshFailed', recheck: () => void save.refresh() }} />
      ) : save.isSuccess ? (
        <p role="status">{t('common.failurePolicy.done')}</p>
      ) : null}
    </section>
  );
}
