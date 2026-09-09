import {
  CancelledError,
  notifyManager,
  useQueries,
  useQuery,
  useQueryClient,
  type UseQueryResult,
} from '@tanstack/react-query';
import { useCallback, useState, useSyncExternalStore } from 'react';
import {
  captureStationSession,
  clearStationSession,
  stationSessionMatches,
} from '@shared/charityManagement';
import { Icon } from '@shared/components/Icon';
import { ApiError, isForbidden, isUnauthorized } from '@shared/query/http';
import { Link } from 'react-router';
import { SafeAnnouncementBody } from './SafeAnnouncementBody';
import {
  announcementDismissalKey,
  normalizeAnnouncementDetail,
  type AnnouncementDetail,
} from './data';
import { collectHomeAnnouncementSummaries } from './homeAnnouncementsData';
import { CoreErrorPanel, CoreLoading, CoreTime, CoreUnavailable } from '../core/components';
import { useCoreCopy } from '../core/copy';
import { coreSessionMatchesAccount } from '../core/queries';
import { coreRequest } from '../core/request';
import type {
  AccountLanguage,
  HomeAnnouncementCapability,
  HomeAnnouncementSummary,
} from '../core/types';
import './homeAnnouncements.css';

const SEVERITY_ICONS = { info: 'info', warning: 'warning', important: 'error' } as const;
const SEVERITY_KEYS = {
  info: 'home.announcement.severity.info',
  warning: 'home.announcement.severity.warning',
  important: 'home.announcement.severity.important',
} as const;
const LANGUAGE_KEYS = {
  zh: 'home.announcement.language.zh',
  en: 'home.announcement.language.en',
} as const;

async function readForAccount<T>(
  queryClient: ReturnType<typeof useQueryClient>,
  accountId: string,
  signal: AbortSignal | undefined,
  request: (guard: () => void) => Promise<T>,
): Promise<T> {
  if (signal?.aborted || !coreSessionMatchesAccount(queryClient, accountId))
    throw new CancelledError();
  const authority = captureStationSession(queryClient, 'steward');
  const guard = () => {
    if (
      signal?.aborted ||
      !coreSessionMatchesAccount(queryClient, accountId) ||
      !stationSessionMatches(queryClient, 'steward', authority)
    )
      throw new CancelledError();
  };
  try {
    guard();
    const result = await request(guard);
    guard();
    return result;
  } catch (error) {
    guard();
    if (isUnauthorized(error) || isForbidden(error)) clearStationSession(queryClient, 'steward');
    throw error;
  }
}

async function loadAnnouncementDetail(
  id: string,
  signal?: AbortSignal,
): Promise<AnnouncementDetail> {
  const response = await coreRequest(`/api/announcements/${encodeURIComponent(id)}`, { signal });
  if (response.status !== 200) {
    throw new ApiError(
      'invalid_response',
      'The server returned an invalid announcement status.',
      response.status,
    );
  }
  return normalizeAnnouncementDetail(response.payload);
}

function sameSummary(a: HomeAnnouncementSummary, b: HomeAnnouncementSummary): boolean {
  return (
    a.epoch === b.epoch &&
    a.id === b.id &&
    a.revision === b.revision &&
    a.effective_language === b.effective_language
  );
}

function writeDismissal(key: string): void {
  if (typeof localStorage === 'undefined') return;
  try {
    localStorage.setItem(key, '1');
  } catch {
    // The account-scoped in-memory set keeps this visit's dismissal.
  }
}

function AnnouncementCard({
  summary,
  detail,
  busy,
  onRetry,
  onDismiss,
}: {
  summary: HomeAnnouncementSummary;
  detail: UseQueryResult<AnnouncementDetail, Error> | undefined;
  busy: boolean;
  onRetry: () => void;
  onDismiss: (summary: HomeAnnouncementSummary) => void;
}) {
  const { t } = useCoreCopy();
  const title = `/announcements/${encodeURIComponent(summary.id)}`;
  return (
    <li className={`home-announcement-card home-announcement-card--${summary.severity}`}>
      <div className="home-announcement-card__top">
        <div
          className={`home-announcement-severity home-announcement-severity--${summary.severity}`}
        >
          <Icon name={SEVERITY_ICONS[summary.severity]} />
          <span>{t(SEVERITY_KEYS[summary.severity])}</span>
        </div>
        <div className="home-announcement-meta">
          <CoreTime value={summary.published_at} />
          {summary.pinned ? <span>{t('home.announcement.pinned')}</span> : null}
          {summary.fallback_from ? (
            <span>
              {t('home.announcement.languageFallback', {
                language: t(LANGUAGE_KEYS[summary.effective_language]),
              })}
            </span>
          ) : null}
        </div>
      </div>
      <h3 className="home-announcement-card__title">
        <Link to={title}>{summary.title}</Link>
      </h3>
      <div className="home-announcement-preview">
        {detail?.isSuccess ? (
          <SafeAnnouncementBody html={detail.data.rendered_body} />
        ) : (
          <p>{summary.excerpt}</p>
        )}
      </div>
      {detail?.isPending ? (
        <p className="home-announcement-detail-status" role="status">
          {t('home.announcement.loadingBody')}
        </p>
      ) : detail?.error ? (
        <div className="home-announcement-detail-error" role="alert">
          <span>{t('home.announcement.detailError')}</span>
          <button type="button" className="btn btn-quiet" disabled={busy} onClick={onRetry}>
            {busy ? t('common.working') : t('common.retry')}
          </button>
        </div>
      ) : null}
      <div className="home-announcement-card__actions">
        <Link className="btn btn-secondary" to={title}>
          {t('home.announcement.viewFull')}
        </Link>
        {summary.dismissible ? (
          <button type="button" className="btn btn-quiet" onClick={() => onDismiss(summary)}>
            {t('home.announcement.hide')}
          </button>
        ) : null}
      </div>
    </li>
  );
}

export function HomeAnnouncements({
  accountId,
  language,
  capability,
  sessionReady = true,
  loadDetail,
}: {
  accountId: string;
  language: AccountLanguage;
  capability: HomeAnnouncementCapability;
  sessionReady?: boolean;
  loadDetail?: (id: string, signal?: AbortSignal) => Promise<AnnouncementDetail>;
}) {
  const { t } = useCoreCopy();
  const queryClient = useQueryClient();
  const sessionIdentity = useCallback(() => {
    if (!coreSessionMatchesAccount(queryClient, accountId)) return '';
    try {
      const authority = captureStationSession(queryClient, 'steward');
      return `${authority.subject}:${authority.generation}`;
    } catch {
      return '';
    }
  }, [queryClient, accountId]);
  const identity = useSyncExternalStore(
    useCallback(
      (notify) => queryClient.getQueryCache().subscribe(notifyManager.batchCalls(notify)),
      [queryClient],
    ),
    sessionIdentity,
    () => '',
  );
  const active = sessionReady && identity !== '';
  const [memoryScope, setMemoryScope] = useState<{ scope: string; keys: Set<string> }>({
    scope: '',
    keys: new Set(),
  });
  const memoryDismissals = memoryScope.scope === accountId ? memoryScope.keys : new Set<string>();
  const loader = capability.state === 'available' ? capability.load : null;
  const queryRoot = ['user', 'core', 'account', accountId, 'home', identity, language];
  const announcementQuery = useQuery({
    queryKey: [...queryRoot, 'announcements', ...[...memoryDismissals].sort()],
    queryFn: ({ signal }) =>
      readForAccount(queryClient, accountId, signal, async (guard) => {
        if (!loader)
          throw new ApiError('capability_unavailable', 'Announcements are unavailable.', 503);
        return collectHomeAnnouncementSummaries(
          async (cursor, pageSignal) => {
            guard();
            const result = await loader(cursor, pageSignal);
            guard();
            return result;
          },
          accountId,
          memoryDismissals,
          signal,
        );
      }),
    enabled: active && capability.state === 'available',
    gcTime: 0,
    retry: false,
  });
  const summaries = active ? (announcementQuery.data ?? []) : [];
  const detailLoader = loadDetail ?? loadAnnouncementDetail;
  const detailQueries = useQueries({
    queries: summaries.map((summary) => ({
      queryKey: [
        ...queryRoot,
        'announcement-detail',
        summary.epoch,
        summary.id,
        summary.revision,
        summary.effective_language,
      ],
      queryFn: ({ signal }: { signal?: AbortSignal }) =>
        readForAccount(queryClient, accountId, signal, async () => {
          const detail = normalizeAnnouncementDetail(await detailLoader(summary.id, signal));
          if (
            detail.epoch !== summary.epoch ||
            detail.id !== summary.id ||
            detail.revision !== summary.revision ||
            detail.effective_language !== summary.effective_language
          ) {
            throw new ApiError(
              'invalid_response',
              'The announcement detail no longer matches its summary.',
              200,
            );
          }
          return detail;
        }),
      enabled: active,
      gcTime: 0,
      retry: false,
    })),
  });

  if (!active) return null;
  if (capability.state === 'available' && announcementQuery.isSuccess && summaries.length === 0)
    return null;

  const dismiss = (summary: HomeAnnouncementSummary) => {
    if (sessionIdentity() !== identity) return;
    const key = announcementDismissalKey(accountId, summary);
    writeDismissal(key);
    setMemoryScope((current) => {
      const keys = current.scope === accountId ? new Set(current.keys) : new Set<string>();
      keys.add(key);
      return { scope: accountId, keys };
    });
  };
  const retryDetail = async (summary: HomeAnnouncementSummary, index: number) => {
    if (sessionIdentity() !== identity) return;
    const refreshed = await announcementQuery.refetch();
    if (sessionIdentity() !== identity || refreshed.isError) return;
    // A changed revision gets its own detail query. An unchanged summary
    // still needs a manual retry because automatic error retries are disabled.
    if (refreshed.data?.some((next) => sameSummary(next, summary)))
      await detailQueries[index]?.refetch();
  };

  return (
    <section
      className="core-card home-announcements"
      aria-labelledby="home-announcements-title"
      aria-busy={announcementQuery.isFetching}
    >
      <div className="core-card__header">
        <h2 id="home-announcements-title">{t('home.announcementsTitle')}</h2>
        <Link to="/announcements">{t('home.announcement.viewAll')}</Link>
      </div>
      {capability.state === 'unavailable' ? (
        <CoreUnavailable compact />
      ) : announcementQuery.isPending ? (
        <CoreLoading compact />
      ) : announcementQuery.error ? (
        <CoreErrorPanel
          compact
          error={announcementQuery.error}
          onRetry={() => void announcementQuery.refetch()}
        />
      ) : (
        <ul className="home-announcement-list">
          {summaries.map((summary, index) => (
            <AnnouncementCard
              key={`${summary.epoch}:${summary.id}:${summary.revision}`}
              summary={summary}
              detail={detailQueries[index]}
              busy={announcementQuery.isFetching || detailQueries[index]?.isFetching === true}
              onRetry={() => void retryDetail(summary, index)}
              onDismiss={dismiss}
            />
          ))}
        </ul>
      )}
    </section>
  );
}
