import { useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { resetOwnerFailure } from '@shared/operations/failureReset';
import { responseOutcomeUnknown } from '@shared/operations/api';
import { ErrorState } from '@shared/components/States';
import { economyKeys } from './queries';

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
    () => client.invalidateQueries({ queryKey: economyKeys.donations }),
    economyKeys.donations,
  );
  const unknown = responseOutcomeUnknown(reset.error);
  return (
    <div className="economy-failure-reset">
      <p className="muted">{t('common.failureReset.help')}</p>
      <button
        type="button"
        className="btn btn-secondary"
        disabled={reset.isPending || (disabled && !unknown)}
        onClick={() => reset.mutate(unknown && reset.variables ? reset.variables : { revision })}
      >
        {unknown ? t('common.failureReset.resume') : t('common.failureReset.title')}
      </button>
      {reset.error ? <ErrorState error={reset.error} /> : null}
      {reset.isSuccess ? <p role="status">{t('common.failureReset.singleDone')}</p> : null}
    </div>
  );
}
