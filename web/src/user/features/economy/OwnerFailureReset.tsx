import { useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { resetOwnerFailure } from '@shared/operations/failureReset';
import { responseOutcomeUnknown } from '@shared/operations/api';
import { ErrorState } from '@shared/components/States';
import { economyKeys } from './queries';
import { FailureResetDetails } from '@shared/components/FailureResetDetails';

export function OwnerFailureReset({
  donationID,
  keyID,
  revision,
  disabled,
}: {
  donationID: string;
  keyID: string;
  revision: string;
  disabled: boolean;
}) {
  const { t } = useTranslation();
  const client = useQueryClient();
  const reset = useRetainedOperation(
    (input: { revision: string }, key) => resetOwnerFailure(donationID, keyID, input.revision, key),
    () =>
      Promise.all([
        client.invalidateQueries({ queryKey: economyKeys.donations }),
        client.invalidateQueries({ queryKey: ['user', 'donation-uses'] }),
      ]),
    economyKeys.donations,
  );
  const unknown = responseOutcomeUnknown(reset.error);
  return (
    <div className="economy-failure-reset">
      <p className="muted">{t('common.failureReset.help')}</p>
      <button
        type="button"
        className="nb-btn nb-btn--secondary"
        disabled={reset.isPending || (disabled && !unknown)}
        onClick={() => reset.mutate(unknown && reset.variables ? reset.variables : { revision })}
      >
        {unknown ? t('common.failureReset.resume') : t('common.failureReset.title')}
      </button>
      {reset.error ? <ErrorState error={reset.error} /> : null}
      {reset.isSuccess ? <FailureResetDetails services={reset.data?.services ?? []} /> : null}
      {reset.isSuccess ? (
        <p role="status">
          {t(
            reset.data?.outcome === 'already_clear'
              ? 'common.failureReset.alreadyClear'
              : 'common.failureReset.singleDone',
          )}
        </p>
      ) : null}
    </div>
  );
}
