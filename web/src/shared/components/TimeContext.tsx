import { type ReactNode } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import {
  browserOffsetMinutes,
  fetchTimeContext,
  fixedOffsetZone,
  timeContextQueryKey,
  type TimeStation,
} from '../time';
import { browserContext, DisplayTimeContext, unavailableSiteContext } from './timeContextValue';
import { useBrowserTimeZone } from './useBrowserTimeZone';

/** Provides a reactive context scoped to this station and its descendants. */
export function TimeContextProvider({
  station,
  children,
}: {
  station: TimeStation;
  children: ReactNode;
}) {
  const query = useQuery({
    queryKey: timeContextQueryKey(station),
    queryFn: ({ signal }) => fetchTimeContext(station as Exclude<TimeStation, 'user'>, signal),
    enabled: station !== 'user',
    staleTime: 30_000,
    retry: false,
  });
  const value =
    station === 'user'
      ? browserContext
      : query.isSuccess && query.data
        ? query.data
        : unavailableSiteContext;
  return <DisplayTimeContext.Provider value={value}>{children}</DisplayTimeContext.Provider>;
}

/** One context notice for a group of time inputs. */
export function TimeContextNotice({ station }: { station: TimeStation }) {
  const { t } = useTranslation();
  const browserZone = useBrowserTimeZone();
  const query = useQuery({
    queryKey: timeContextQueryKey(station),
    queryFn: ({ signal }) => fetchTimeContext(station as Exclude<TimeStation, 'user'>, signal),
    enabled: station !== 'user',
    staleTime: 30_000,
    retry: false,
  });
  if (station === 'user')
    return (
      <p className="field-help time-context-notice" role="status">
        {browserZone
          ? t('common.time.localZone', { zone: browserZone })
          : t('common.time.fallback')}
      </p>
    );
  const offset = query.isSuccess ? query.data?.offset_minutes : null;
  const browserOffset = browserOffsetMinutes(browserZone);
  return (
    <p className="field-help time-context-notice" role="status">
      {query.isError ? (
        <>
          {t('common.time.contextFailed')}{' '}
          <button type="button" className="btn btn-link" onClick={() => void query.refetch()}>
            {t('common.retry')}
          </button>
        </>
      ) : !query.isSuccess ? (
        t('common.time.contextLoading')
      ) : offset === null || offset === undefined ? (
        t('common.time.siteUnavailable')
      ) : (
        <>
          {t('common.time.zone', { zone: fixedOffsetZone(offset) })}
          {browserOffset !== null && browserOffset !== offset && (
            <> · {t('common.time.siteBrowserNotice')}</>
          )}
        </>
      )}
    </p>
  );
}
