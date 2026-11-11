import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Icon } from './Icon';
import './queryFeedback.css';

export function useLongPending(pending: boolean) {
  const [long, setLong] = useState(false);
  useEffect(() => {
    if (!pending) return;
    const timer = window.setTimeout(() => setLong(true), 8_000);
    return () => {
      window.clearTimeout(timer);
      setLong(false);
    };
  }, [pending]);
  return pending && long;
}

/** Feedback beside retained results. Polling views only announce a changed query. */
export function QueryFeedback({
  query,
  quietRefetch = false,
}: {
  query: {
    isFetching: boolean;
    data: unknown;
    isPlaceholderData?: boolean;
    error?: unknown;
    retained?: boolean;
  };
  quietRefetch?: boolean;
}) {
  const { t } = useTranslation();
  const failed = !query.isFetching && query.retained === true && Boolean(query.error);
  const active =
    query.data !== undefined &&
    (failed ||
      (query.isFetching &&
        (!quietRefetch || query.isPlaceholderData === true || query.retained === true)));
  const long = useLongPending(active && query.isFetching);
  if (!active) return null;
  return (
    <div
      className="nb-query-feedback"
      data-pending={query.isFetching ? true : undefined}
      role="status"
      aria-live="polite"
      aria-atomic="true"
    >
      <span className="nb-query-feedback__indicator" aria-hidden="true">
        <Icon name="spark" />
      </span>
      <span>
        {t(
          failed
            ? 'common.query.failedRetained'
            : long
              ? 'common.query.stillUpdating'
              : 'common.query.updating',
        )}
      </span>
    </div>
  );
}
