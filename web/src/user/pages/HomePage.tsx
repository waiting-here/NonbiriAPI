import {
  CancelledError,
  useMutation,
  useQuery,
  useQueryClient,
  type QueryClient,
} from '@tanstack/react-query';
import { useState } from 'react';
import { Link } from 'react-router';
import { PageHeader } from '@shared/components/States';
import { isNotFoundError, isUnauthorized } from '@shared/query/http';
import {
  CoreErrorPanel,
  CoreLoading,
  CoreProfileGate,
  CoreTime,
  CoreUnavailable,
  ExactCount,
  ExactCredits,
  StatusPill,
} from '../features/core/components';
import { useCoreCopy } from '../features/core/copy';
import { CORE_ROUTE_PATHS } from '../features/core/descriptors';
import { CapabilityUnavailableError, productionHomeAdapters } from '../features/core/adapters';
import {
  coreKeys,
  coreSessionMatchesAccount,
  useCoreMe,
  useCoreSession,
} from '../features/core/queries';
import { isConflict, isOutcomeUnknown } from '../features/core/request';
import type {
  HomeAdapters,
  HomeCheckinResult,
  HomeCheckinStatus,
  HomeGameSummary,
  UserProfile,
} from '../features/core/types';
import '../features/core/core.css';

const GAME_PATHS: Record<HomeGameSummary['route_id'], string> = {
  'game-fishing': '/games/fishing',
  'game-linklink': '/games/linklink',
  'game-rps': '/games/rps',
};

const GAME_LABELS = {
  'game-fishing': 'home.gameFishing',
  'game-linklink': 'home.gameLinklink',
  'game-rps': 'home.gameRps',
} as const;

function isEnabledCheckin(
  value: HomeCheckinStatus | undefined,
): value is Extract<HomeCheckinStatus, { enabled: true }> {
  return value?.enabled === true;
}

interface CommittedCheckin {
  result: HomeCheckinResult;
  authority: Extract<HomeCheckinStatus, { enabled: true }>;
}

function checkinMilli(value: string): bigint {
  const negative = value.startsWith('-');
  const unsigned = negative ? value.slice(1) : value;
  const [whole = '0', fraction = ''] = unsigned.split('.');
  const milli = BigInt(whole) * 1_000n + BigInt(fraction.padEnd(3, '0') || '0');
  return negative ? -milli : milli;
}

function homeAccountCurrent(queryClient: QueryClient, accountId: string): boolean {
  return (
    queryClient.getQueryData(coreKeys.session) === undefined ||
    coreSessionMatchesAccount(queryClient, accountId)
  );
}

async function accountScopedHomeLoad<T>(
  queryClient: QueryClient,
  accountId: string,
  load: (signal?: AbortSignal) => Promise<T>,
  signal?: AbortSignal,
): Promise<T> {
  if (!homeAccountCurrent(queryClient, accountId)) throw new CancelledError();
  const result = await load(signal);
  if (!homeAccountCurrent(queryClient, accountId)) throw new CancelledError();
  return result;
}

function SignedOutHome() {
  const { t } = useCoreCopy();
  return (
    <div className="page core-page core-stack">
      <PageHeader
        icon="home"
        title={t('home.signedOutTitle')}
        description={t('home.signedOutBody')}
      />
      <section className="core-card">
        <div className="core-card__header">
          <div>
            <h2>{t('home.signedOutTitle')}</h2>
            <p className="core-muted">{t('home.signedOutBody')}</p>
          </div>
          <a className="btn btn-primary" href="/api/auth/discord/start">
            {t('home.signIn')}
          </a>
        </div>
      </section>
    </div>
  );
}

function ProfileCard({ user }: { user: UserProfile }) {
  const { t } = useCoreCopy();
  return (
    <section className="core-card">
      <div className="core-card__header">
        <h2>{t('home.profileTitle')}</h2>
      </div>
      <dl className="core-detail-list">
        <div>
          <dt>{t('home.username')}</dt>
          <dd>{user.guild_nick || user.username}</dd>
        </div>
        <div>
          <dt>{t('home.accountStatus')}</dt>
          <dd>
            <StatusPill tone={user.is_banned ? 'danger' : 'success'}>
              {user.is_banned ? t('home.banned') : t('home.active')}
            </StatusPill>
          </dd>
        </div>
        <div>
          <dt>{t('common.created')}</dt>
          <dd>
            <CoreTime value={user.created_at} />
          </dd>
        </div>
      </dl>
    </section>
  );
}

function EconomyCard({ accountId }: { accountId: string }) {
  const { t } = useCoreCopy();
  const me = useCoreMe(accountId);
  return (
    <section className="core-card">
      <div className="core-card__header">
        <h2>{t('home.economyTitle')}</h2>
        <Link className="btn btn-secondary" to="/credits">{t('home.creditHistory')}</Link>
      </div>
      {me.isPending ? (
        <CoreLoading compact />
      ) : me.error ? (
        <CoreErrorPanel error={me.error} compact onRetry={() => void me.refetch()} />
      ) : (
        <div className="core-metrics">
          <div className="core-metric">
            <span>{t('home.balance')}</span>
            <strong>
              <ExactCredits value={me.data.user.balance} />
            </strong>
          </div>
          <div className="core-metric">
            <span>{t('home.donationCredit')}</span>
            <strong>
              <ExactCredits value={me.data.user.donation_credit} />
            </strong>
          </div>
          <div className="core-metric">
            <span>{t('home.level')}</span>
            <strong>
              {me.data.user.level_display_name === `Lv${me.data.user.effective_level}`
                ? me.data.user.level_display_name
                : t('home.levelValue', {
                    level: me.data.user.effective_level,
                    name: me.data.user.level_display_name,
                  })}
            </strong>
          </div>
        </div>
      )}
    </section>
  );
}

function UsageCard({ user }: { user: UserProfile }) {
  const { t } = useCoreCopy();
  return (
    <section className="core-card">
      <div className="core-card__header">
        <h2>{t('home.usageTitle')}</h2>
      </div>
      <div className="core-metrics">
        <div className="core-metric">
          <span>{t('home.requests')}</span>
          <strong>
            <ExactCount value={user.usage.total_requests} />
          </strong>
        </div>
        <div className="core-metric">
          <span>{t('home.promptTokens')}</span>
          <strong>
            <ExactCount value={user.usage.total_prompt_tokens} />
          </strong>
        </div>
        <div className="core-metric">
          <span>{t('home.outputTokens')}</span>
          <strong>
            <ExactCount value={user.usage.total_output_tokens} />
          </strong>
        </div>
        <div className="core-metric">
          <span>{t('home.unknownUsage')}</span>
          <strong>
            <ExactCount value={user.usage.total_unknown_usage_requests} />
          </strong>
        </div>
      </div>
    </section>
  );
}

function CheckinCard({
  accountId,
  effectiveLevel,
  capability,
}: {
  accountId: string;
  effectiveLevel: number;
  capability: HomeAdapters['checkin'];
}) {
  const { t } = useCoreCopy();
  const queryClient = useQueryClient();
  const [committed, setCommitted] = useState<CommittedCheckin | null>(null);
  const [outcomeUnknown, setOutcomeUnknown] = useState(false);
  const [reconciling, setReconciling] = useState(false);
  const loader = capability.state === 'available' ? capability.load : null;
  const submitter = capability.state === 'available' ? capability.submit : null;
  const status = useQuery({
    queryKey: coreKeys.home(accountId, 'checkin'),
    queryFn: ({ signal }) => {
      if (!loader) throw new CapabilityUnavailableError();
      return accountScopedHomeLoad(queryClient, accountId, loader, signal);
    },
    enabled: capability.state === 'available',
    retry: false,
  });
  const mutation = useMutation({
    mutationFn: async () => {
      if (!submitter) throw new CapabilityUnavailableError();
      if (!homeAccountCurrent(queryClient, accountId)) throw new CancelledError();
      const result = await submitter();
      if (!homeAccountCurrent(queryClient, accountId)) throw new CancelledError();
      return result;
    },
    retry: false,
  });

  const refreshAuthority = async () => {
    setReconciling(true);
    try {
      const authority = await status.refetch();
      if (authority.isSuccess) {
        setOutcomeUnknown(false);
        mutation.reset();
      }
    } finally {
      setReconciling(false);
    }
  };

  const submit = async () => {
    setCommitted(null);
    setOutcomeUnknown(false);
    mutation.reset();
    try {
      const result = await mutation.mutateAsync();
      if (enabledAuthority) {
        setCommitted({
          result,
          authority: {
            ...enabledAuthority,
            checked_in_today: true,
            balance: result.balance,
          },
        });
      }
      await status.refetch();
    } catch (error) {
      if (isOutcomeUnknown(error)) {
        setOutcomeUnknown(true);
        await refreshAuthority();
      } else if (isConflict(error)) {
        await refreshAuthority();
      }
    }
  };

  const authority = status.data;
  const enabledAuthority = isEnabledCheckin(authority) ? authority : null;
  const displayedAuthority = enabledAuthority ?? committed?.authority ?? null;
  const checkedIn =
    committed !== null && status.error !== null
      ? true
      : (enabledAuthority?.checked_in_today ?? committed !== null);
  const capReached =
    displayedAuthority !== null &&
    effectiveLevel < 3 &&
    displayedAuthority.balance_cap !== '0' &&
    checkinMilli(displayedAuthority.balance) >= checkinMilli(displayedAuthority.balance_cap);
  return (
    <section className="core-card">
      <div className="core-card__header">
        <h2>{t('home.checkinTitle')}</h2>
      </div>
      {capability.state === 'unavailable' ? (
        <CoreUnavailable compact />
      ) : outcomeUnknown ? (
        <div className="core-state core-state--warning core-state--compact" role="status">
          <div>
            <strong>{t('common.unknown')}</strong>
            <p>{t('common.outcomeUnknown')}</p>
            <button
              type="button"
              className="btn btn-secondary"
              disabled={reconciling}
              onClick={() => void refreshAuthority()}
            >
              {reconciling ? t('common.working') : t('common.reconcile')}
            </button>
          </div>
        </div>
      ) : status.isPending ? (
        <CoreLoading compact />
      ) : status.error && !displayedAuthority ? (
        <CoreErrorPanel error={status.error} compact onRetry={() => void status.refetch()} />
      ) : !displayedAuthority ? (
        <p className="core-muted">{t('home.checkin.unavailable')}</p>
      ) : (
        <>
          <div className="core-metrics">
            <div className="core-metric">
              <span>{t('home.checkin.today')}</span>
              <strong>
                {checkedIn ? t('home.checkin.checkedIn') : t('home.checkin.notCheckedIn')}
              </strong>
            </div>
            <div className="core-metric">
              <span>{t('home.balance')}</span>
              <strong>
                <ExactCredits value={committed?.result.balance ?? displayedAuthority.balance} />
              </strong>
            </div>
            <div className="core-metric">
              <span>{t('home.checkin.awardRange')}</span>
              <strong>
                <ExactCredits value={displayedAuthority.award_min} />–
                <ExactCredits value={displayedAuthority.award_max} />
              </strong>
            </div>
            <div className="core-metric">
              <span>{t('home.checkin.threshold')}</span>
              <strong>
                {displayedAuthority.balance_cap === '0' ? (
                  t('home.checkin.thresholdNone')
                ) : (
                  <ExactCredits value={displayedAuthority.balance_cap} />
                )}
              </strong>
            </div>
          </div>
          {effectiveLevel < 3 && displayedAuthority.balance_cap !== '0' ? (
            <p className="core-muted">{t('home.checkin.thresholdHint')}</p>
          ) : null}
          {committed ? (
            <p className="core-status-message" role="status">
              {t('home.checkin.done', {
                award: committed.result.award,
                credits: committed.result.balance,
              })}
            </p>
          ) : null}
          {committed && status.error ? (
            <div className="core-state core-state--warning core-state--compact" role="alert">
              <div>
                <strong>{t('common.errorTitle')}</strong>
                <p>{t('home.checkinRefreshFailed')}</p>
                <button
                  type="button"
                  className="btn btn-secondary"
                  disabled={status.isFetching}
                  onClick={() => void status.refetch()}
                >
                  {status.isFetching ? t('common.working') : t('common.refresh')}
                </button>
              </div>
            </div>
          ) : null}
          {!committed && status.error ? (
            <CoreErrorPanel error={status.error} compact onRetry={() => void status.refetch()} />
          ) : null}
          {mutation.error && !isOutcomeUnknown(mutation.error) && !isConflict(mutation.error) ? (
            <CoreErrorPanel error={mutation.error} compact />
          ) : null}
          <button
            type="button"
            className="btn btn-primary"
            disabled={
              checkedIn ||
              mutation.isPending ||
              status.isFetching ||
              status.error !== null ||
              capReached
            }
            onClick={() => void submit()}
          >
            {mutation.isPending ? t('common.working') : t('home.checkin.submit')}
          </button>
        </>
      )}
    </section>
  );
}

function CapabilitySections({
  accountId,
  effectiveLevel,
  adapters,
}: {
  accountId: string;
  effectiveLevel: number;
  adapters: HomeAdapters;
}) {
  const { t } = useCoreCopy();
  const queryClient = useQueryClient();
  const gamesLoader = adapters.games.state === 'available' ? adapters.games.load : null;
  const announcementsLoader =
    adapters.announcements.state === 'available' ? adapters.announcements.load : null;
  const games = useQuery({
    queryKey: coreKeys.home(accountId, 'games'),
    queryFn: ({ signal }) => {
      if (!gamesLoader) throw new CapabilityUnavailableError();
      return accountScopedHomeLoad(queryClient, accountId, gamesLoader, signal);
    },
    enabled: adapters.games.state === 'available',
    retry: false,
  });
  const announcements = useQuery({
    queryKey: coreKeys.home(accountId, 'announcements'),
    queryFn: ({ signal }) => {
      if (!announcementsLoader) throw new CapabilityUnavailableError();
      return accountScopedHomeLoad(queryClient, accountId, announcementsLoader, signal);
    },
    enabled: adapters.announcements.state === 'available',
    retry: false,
  });
  return (
    <>
      <CheckinCard
        key={accountId}
        accountId={accountId}
        effectiveLevel={effectiveLevel}
        capability={adapters.checkin}
      />

      {adapters.games.state === 'unavailable' ? (
        <section className="core-card">
          <div className="core-card__header">
            <h2>{t('home.gamesTitle')}</h2>
          </div>
          <CoreUnavailable compact />
        </section>
      ) : games.isSuccess && games.data.length === 0 ? null : (
        <section className="core-card">
          <div className="core-card__header">
            <h2>{t('home.gamesTitle')}</h2>
          </div>
          {games.isPending ? (
            <CoreLoading compact />
          ) : games.error ? (
            <CoreErrorPanel compact error={games.error} onRetry={() => void games.refetch()} />
          ) : (
            <div className="core-choice-grid">
              {games.data.map((item) => (
                <Link
                  key={`${item.route_id}:${item.kind}:${item.resource_id}`}
                  className="core-choice"
                  to={GAME_PATHS[item.route_id]}
                >
                  <strong>{t(GAME_LABELS[item.route_id])}</strong>
                  <span>{item.kind === 'continue' ? t('home.continue') : t('home.view')}</span>
                </Link>
              ))}
            </div>
          )}
        </section>
      )}

      {adapters.announcements.state === 'unavailable' ? (
        <section className="core-card">
          <div className="core-card__header">
            <h2>{t('home.announcementsTitle')}</h2>
          </div>
          <CoreUnavailable compact />
        </section>
      ) : announcements.isSuccess && announcements.data.length === 0 ? null : (
        <section className="core-card">
          <div className="core-card__header">
            <h2>{t('home.announcementsTitle')}</h2>
          </div>
          {announcements.isPending ? (
            <CoreLoading compact />
          ) : announcements.error ? (
            <CoreErrorPanel
              compact
              error={announcements.error}
              onRetry={() => void announcements.refetch()}
            />
          ) : (
            <ul className="core-endpoint-list">
              {announcements.data.map((item) => (
                <li key={item.id} className="core-endpoint-card">
                  <Link to={`/announcements/${encodeURIComponent(item.id)}`}>
                    <strong>{item.title}</strong>
                  </Link>
                  <p>{item.excerpt}</p>
                </li>
              ))}
            </ul>
          )}
        </section>
      )}
    </>
  );
}

export function HomeDashboard({
  user,
  adapters = productionHomeAdapters,
}: {
  user: UserProfile;
  adapters?: HomeAdapters;
}) {
  const { t } = useCoreCopy();
  return (
    <div className="page core-page core-stack">
      <PageHeader
        icon="home"
        title={t('home.title', { name: user.guild_nick || user.username })}
        description={t('home.description')}
      />
      <div className="core-grid core-grid--wide">
        <ProfileCard user={user} />
        <EconomyCard accountId={user.id} />
      </div>
      <UsageCard user={user} />
      <CapabilitySections
        accountId={user.id}
        effectiveLevel={user.effective_level}
        adapters={adapters}
      />
      <section className="core-card">
        <div className="core-card__header">
          <h2>{t('home.quickTitle')}</h2>
        </div>
        <div className="core-choice-grid">
          <Link className="core-choice" to={CORE_ROUTE_PATHS.endpoints}>
            {t('home.endpoints')}
          </Link>
          <Link className="core-choice" to={CORE_ROUTE_PATHS.models}>
            {t('home.models')}
          </Link>
          <Link className="core-choice" to={CORE_ROUTE_PATHS.keys}>
            {t('home.callerKey')}
          </Link>
          <Link className="core-choice" to="/activities">
            {t('home.activities')}
          </Link>
          <Link className="core-choice" to="/games">
            {t('home.games')}
          </Link>
        </div>
      </section>
    </div>
  );
}

export function HomePage() {
  const session = useCoreSession();
  if (session.isPending)
    return (
      <div className="page core-page">
        <CoreLoading />
      </div>
    );
  if (session.error) {
    if (isUnauthorized(session.error) || isNotFoundError(session.error)) return <SignedOutHome />;
    return (
      <div className="page core-page">
        <CoreErrorPanel error={session.error} onRetry={() => void session.refetch()} />
      </div>
    );
  }
  if (session.data === null) return <SignedOutHome />;
  return (
    <CoreProfileGate
      key={session.data.accountId}
      session={session.data}
      signedOut={<SignedOutHome />}
    >
      {(user) => <HomeDashboard key={user.id} user={user} />}
    </CoreProfileGate>
  );
}
