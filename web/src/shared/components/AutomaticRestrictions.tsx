import { Button } from '@shared/components/ui/Button';
import { useEffect, useState } from 'react';
import { useInfiniteQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { type AutomaticRestriction } from '@shared/operations/restrictions';
import { getAccessDenial } from '@shared/operations/accessDenial';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import { useDateTimeFormatter } from '@shared/utils/datetime';
import { ErrorState, LoadingState } from './States';
import { ReasonText } from './ReasonText';

export function AutomaticRestrictions({ restrictions }: { restrictions: AutomaticRestriction[] }) {
  const formatDateTime = useDateTimeFormatter();
  const { t } = useTranslation();
  const [now, setNow] = useState(() => Math.floor(Date.now() / 1000));
  useEffect(() => {
    if (!restrictions.length) return;
    const timer = window.setInterval(() => setNow(Math.floor(Date.now() / 1000)), 1000);
    return () => window.clearInterval(timer);
  }, [restrictions.length]);
  const current = restrictions.filter((r) => r.ends_at === null || r.ends_at > now);
  if (!current.length) return null;
  return (
    <section className="nb-card" aria-label={t('common.reasons.automaticTitle')}>
      <h2>{t('common.reasons.automaticTitle')}</h2>
      {current.map((r) => (
        <div key={r.kind}>
          <strong>
            {t(r.kind === 'ban' ? 'common.reasons.ban' : 'common.reasons.charitySuspend')}
          </strong>
          <ReasonText reason={r.reason} reasonCode={r.reason_code} />
          <p>
            {t('common.reasons.ends')}:{' '}
            {r.ends_at === null ? t('common.reasons.noEnd') : formatDateTime(r.ends_at)}
          </p>
        </div>
      ))}
    </section>
  );
}

export function LoginRestrictions() {
  const { t } = useTranslation();
  const formatDateTime = useDateTimeFormatter();
  const enabled = window.location.pathname === '/access-denied';
  const query = useInfiniteQuery({
    queryKey: ['login-denial'],
    queryFn: ({ pageParam, signal }) => getAccessDenial(pageParam, signal),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (page) => page.next_cursor,
    enabled,
    retry: false,
    gcTime: 0,
    staleTime: 0,
    refetchOnWindowFocus: false,
  });
  useEffect(() => {
    if (window.location.pathname === '/access-denied' && window.location.hash) {
      window.history.replaceState(
        window.history.state,
        '',
        window.location.pathname + window.location.search,
      );
    }
  }, []);
  if (!enabled) return null;
  if (query.isPending) return <LoadingState />;
  if (isUnauthorized(query.error) || isForbidden(query.error))
    return <p>{t('common.reasons.recheckLogin')}</p>;
  if (query.error) return <ErrorState error={query.error} onRetry={() => void query.refetch()} />;
  if (query.data?.pages[0]?.restricted === false)
    return <p role="status">{t('common.reasons.cleared')}</p>;
  const kinds = {
    ban: 'common.reasons.ban',
    blacklist: 'common.reasons.blacklist',
    blacklist_note: 'common.reasons.additionalNote',
  } as const;
  return (
    <section className="nb-card ops-stack" aria-label={t('common.reasons.accessTitle')}>
      <h2>{t('common.reasons.accessTitle')}</h2>
      {query.data?.pages
        .flatMap((page) => page.items)
        .map((item, index) => (
          <article key={index} className="ops-subcard">
            <h3>{t(kinds[item.kind])}</h3>
            <ReasonText
              reason={item.reason}
              automatic={item.automatic_reason}
              reasonCode={item.automatic?.reason_code}
              reasonCodes={item.reason_codes}
            />
            <p>
              {t('common.reasons.started')}: {formatDateTime(item.started_at)}
            </p>
            <p>
              {t('common.reasons.ends')}:{' '}
              {item.ends_at === null ? t('common.reasons.noEnd') : formatDateTime(item.ends_at)}
            </p>
          </article>
        ))}
      {query.hasNextPage ? (
        <Button
          type="button"

          disabled={query.isFetchingNextPage}
          onClick={() => void query.fetchNextPage()}
        >
          {t('common.reasons.more')}
        </Button>
      ) : null}
    </section>
  );
}
