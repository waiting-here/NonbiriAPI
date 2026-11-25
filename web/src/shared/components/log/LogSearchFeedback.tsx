import { Icon, type IconName } from '@shared/components/Icon';
import { ErrorState } from '@shared/components/States';
import { Button } from '@shared/components/ui/Button';
import { useTranslation } from 'react-i18next';
import type { LogSearchStatus } from './numberedQueries';

const FEEDBACK = {
  loading: { icon: 'search', title: 'common.operations.logs.search.loading' },
  long: { icon: 'search', title: 'common.operations.logs.search.long' },
  complete: { icon: 'check', title: 'common.operations.logs.search.complete' },
  canceling: { icon: 'close', title: 'common.operations.logs.search.canceling' },
  canceled: { icon: 'close', title: 'common.operations.logs.search.canceled' },
  'cancel-error': { icon: 'error', title: 'common.operations.logs.search.cancelError' },
} satisfies Record<Exclude<LogSearchStatus, 'error'>, { icon: IconName; title: string }>;

export function LogSearchFeedback({
  state,
  hasEarlierResults,
  error,
  onCancel,
  onRetry,
  onSearchAgain,
}: {
  state: LogSearchStatus;
  hasEarlierResults: boolean;
  error: Error | null;
  onCancel: () => void;
  onRetry: () => void;
  onSearchAgain: () => void;
}) {
  const { t } = useTranslation();
  const earlier = hasEarlierResults ? (
    <span className="log-search-feedback__hint">
      {t('common.operations.logs.search.earlierResults')}
    </span>
  ) : null;
  if (state === 'error')
    return (
      <div className="ops-stack" data-log-search-state={state}>
        <ErrorState error={error} onRetry={onRetry} />
        {earlier}
      </div>
    );

  const { icon, title } = FEEDBACK[state];
  const action =
    state === 'canceled'
      ? 'common.operations.logs.search.again'
      : state === 'cancel-error'
        ? 'common.operations.logs.search.retryCancel'
        : state === 'long' || state === 'canceling'
          ? 'common.operations.logs.search.cancel'
          : undefined;
  return (
    <div className="log-search-feedback" data-log-search-state={state}>
      <div
        className="log-search-feedback__status"
        role={state === 'cancel-error' ? 'alert' : 'status'}
        aria-atomic="true"
      >
        <span className="log-search-feedback__icon" aria-hidden="true">
          <Icon name={icon} />
        </span>
        <div>
          <strong>{t(title)}</strong>
          {state === 'long' ? (
            <span className="log-search-feedback__hint">
              {t('common.operations.logs.search.longHint')}
            </span>
          ) : null}
          {earlier}
        </div>
      </div>
      {action ? (
        <Button
          type="button"
          aria-disabled={state === 'canceling' || undefined}
          onClick={state === 'canceled' ? onSearchAgain : onCancel}
        >
          {t(action)}
        </Button>
      ) : null}
    </div>
  );
}
