import { useId, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { charityKeys } from '@shared/operations/charity';
import { responseOutcomeUnknown } from '@shared/operations/api';
import { saveFailurePolicy, validFailureThreshold } from '@shared/operations/failurePolicy';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { ErrorState } from './States';
import { useCharityModelScope } from './charityModelScopeContext';
import './FailurePolicyControl.css';

export function FailurePolicyWarning() {
  const { t } = useTranslation();
  return (
    <p className="failure-policy-warning" role="status">
      <span aria-hidden="true">⚠</span> {t('common.failurePolicy.zeroWarning')}
    </p>
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
  const id = useId();
  return (
    <div className="failure-policy-input">
      <label htmlFor={id}>{t('common.failurePolicy.label')}</label>
      <input
        id={id}
        type="text"
        inputMode="numeric"
        maxLength={39}
        value={value}
        disabled={disabled}
        aria-describedby={id + '-help'}
        aria-invalid={!validFailureThreshold(value)}
        onClick={(event) => event.stopPropagation()}
        onChange={(event) => onChange(event.target.value)}
      />
      <small id={id + '-help'}>{t('common.failurePolicy.help')}</small>
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
  refresh,
}: {
  role: 'owner' | 'admin' | 'steward';
  donationID: string;
  keyID: string;
  revision: string;
  threshold: string;
  refresh?: () => Promise<unknown>;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const [draft, setDraft] = useState(threshold);
  const modelID = useCharityModelScope();
  const root = role === 'owner' ? ['user', 'economy', 'donations'] : charityKeys.root(role);
  const save = useRetainedOperation(
    (input: { expected_revision: string; failure_disable_threshold: string }, key) =>
      saveFailurePolicy(role, donationID, keyID, input, key, modelID),
    () => (refresh ? refresh() : client.invalidateQueries({ queryKey: root })),
    root,
  );
  const unknown = responseOutcomeUnknown(save.error);
  return (
    <section className="failure-policy-control">
      <p>{t('common.failurePolicy.saved', { value: threshold })}</p>
      {threshold === '0' && draft !== '0' ? <FailurePolicyWarning /> : null}
      <FailureThresholdInput
        value={draft}
        onChange={setDraft}
        disabled={save.isPending || unknown}
      />
      <p className="muted">{t('common.failurePolicy.recalculate')}</p>
      <button
        type="button"
        className="btn btn-secondary"
        disabled={
          save.isPending || (!unknown && (!validFailureThreshold(draft) || draft === threshold))
        }
        onClick={() =>
          save.mutate(
            unknown && save.variables
              ? save.variables
              : {
                  expected_revision: revision,
                  failure_disable_threshold: draft,
                },
          )
        }
      >
        {t(unknown ? 'common.failureReset.resume' : 'common.failurePolicy.save')}
      </button>
      {save.error ? <ErrorState error={save.error} /> : null}
      {save.isSuccess ? <p role="status">{t('common.failurePolicy.done')}</p> : null}
    </section>
  );
}
