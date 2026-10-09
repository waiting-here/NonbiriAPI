import {
  CancelledError,
  useMutation,
  useQuery,
  useQueryClient,
  type QueryClient,
} from '@tanstack/react-query';
import { useState } from 'react';
import { Link } from 'react-router';
import { useRegisteredCopy } from '@shared/i18n/useRegisteredCopy';
import { ApiAddressCopy, markOnboarding, useOnboardingFlag } from '../features/core/onboarding';
import { useNumberedModels } from '../features/core/modelNumberedQueries';
import { useCharityCatalog } from '../features/economy/catalog';
import { PageHeader } from '@shared/components/States';
import { isNotFoundError, isUnauthorized } from '@shared/query/http';
import {
  CoreErrorPanel,
  CoreLoading,
  CoreProfileGate,
  CoreUnavailable,
  ExactCredits,
} from '../features/core/components';
import { useCoreCopy } from '../features/core/copy';
import { CapabilityUnavailableError, productionHomeAdapters } from '../features/core/adapters';
import {
  coreKeys,
  coreSessionMatchesAccount,
  useCoreMe,
  useCoreSession,
  useCallerKey,
} from '../features/core/queries';
import { isConflict, isOutcomeUnknown } from '../features/core/request';
import type {
  CreditAsset,
  HomeAdapters,
  HomeCheckinResult,
  HomeCheckinStatus,
  HomeGameSummary,
  UserProfile,
} from '../features/core/types';
import { HomeAnnouncements } from '../features/operations/HomeAnnouncements';
import '../features/core/core.css';
import '../features/core/home.css';

const pageCopyKeys = {
  'user.home.apiAccess': 'user.home.apiAccess',
  'user.home.calls': 'user.home.calls',
  'user.home.charity': 'user.home.charity',
  'user.home.claimedToday': 'user.home.claimedToday',
  'user.home.availableTomorrow': 'user.home.availableTomorrow',
  'user.home.clientGuide': 'user.home.clientGuide',
  'user.home.clientHelp': 'user.home.clientHelp',
  'user.home.clientStep': 'user.home.clientStep',
  'user.home.collapseHelp': 'user.home.collapseHelp',
  'user.home.create': 'user.home.create',
  'user.home.getStarted': 'user.home.getStarted',
  'user.home.hide': 'user.home.hide',
  'user.home.introAddress': 'user.home.introAddress',
  'user.home.introAddressBody': 'user.home.introAddressBody',
  'user.home.introCharity': 'user.home.introCharity',
  'user.home.introCharityBody': 'user.home.introCharityBody',
  'user.home.introGames': 'user.home.introGames',
  'user.home.introGamesBody': 'user.home.introGamesBody',
  'user.home.keyHelp': 'user.home.keyHelp',
  'user.home.keyStep': 'user.home.keyStep',
  'user.home.modelHelp': 'user.home.modelHelp',
  'user.home.modelStep': 'user.home.modelStep',
  'user.home.noCalls': 'user.home.noCalls',
  'user.home.progress': 'user.home.progress',
  'user.home.services': 'user.home.services',
  'user.home.setupDone': 'user.home.setupDone',
  'user.home.usage': 'user.home.usage',
  'user.home.view': 'user.home.view',
} as const;

const GAME_PATHS: Record<HomeGameSummary['route_id'], string> = {
  'game-fishing': '/games/fishing',
  'game-linklink': '/games/linklink',
  'game-rps': '/games/rps',
  'game-bidding': '/games/bidding',
  'game-blackjack': '/games/blackjack',
  'game-likes': '/games/likes',
  'game-gwent': '/games/gwent',
  'game-lake-notes': '/games/lake-notes',
  'game-steady-catch': '/games/steady-catch',
};

const GAME_LABELS = {
  'game-fishing': 'home.gameFishing',
  'game-linklink': 'home.gameLinklink',
  'game-rps': 'home.gameRps',
  'game-bidding': 'home.gameBidding',
  'game-blackjack': 'home.gameBlackjack',
  'game-likes': 'home.gameLikes',
  'game-gwent': 'home.gameGwent',
  'game-lake-notes': 'home.gameLakeNotes',
  'game-steady-catch': 'home.gameSteadyCatch',
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
  const { t: text } = useRegisteredCopy(pageCopyKeys);
  return (
    <div className="page core-page core-stack home-page">
      <section className="nb-panel home-intro">
        <h1>{t('home.signedOutTitle')}</h1>
        <p>{t('home.signedOutBody')}</p>
        <div className="home-intro-points">
          <div>
            <h2>{text('user.home.introCharity')}</h2>
            <p>{text('user.home.introCharityBody')}</p>
          </div>
          <div>
            <h2>{text('user.home.introAddress')}</h2>
            <p>{text('user.home.introAddressBody')}</p>
          </div>
          <div>
            <h2>{text('user.home.introGames')}</h2>
            <p>{text('user.home.introGamesBody')}</p>
          </div>
        </div>
        <a className="nb-btn nb-btn--primary" href="/api/auth/discord/start">
          {t('home.signIn')}
        </a>
      </section>
    </div>
  );
}

function EconomyCard({ accountId }: { accountId: string }) {
  const { t } = useCoreCopy();
  const { t: text } = useRegisteredCopy(pageCopyKeys);
  const me = useCoreMe(accountId);
  return (
    <section className="nb-panel home-wallet">
      <div className="nb-between">
        <h2>{t('home.economyTitle')}</h2>
        <Link to="/credits">{t('home.creditHistory')} →</Link>
      </div>
      {me.isPending ? (
        <CoreLoading compact />
      ) : me.error ? (
        <CoreErrorPanel error={me.error} compact onRetry={() => void me.refetch()} />
      ) : (
        <>
          <div className="nb-stats">
            <div className="nb-stat">
              <div className="nb-stat__label">{t('home.balance')}</div>
              <div className="nb-stat__value">
                <ExactCredits value={me.data.user.balance} />
              </div>
            </div>
            <div className="nb-stat">
              <div className="nb-stat__label">{t('home.gameBalance')}</div>
              <div className="nb-stat__value">
                <ExactCredits value={me.data.user.game_balance} />
              </div>
            </div>
          </div>
          <dl className="nb-facts nb-facts--inline nb-small">
            <div>
              <dt>{t('home.level')}</dt>
              <dd>
                {me.data.user.level_display_name === `Lv${me.data.user.effective_level}`
                  ? me.data.user.level_display_name
                  : t('home.levelValue', {
                      level: me.data.user.effective_level,
                      name: me.data.user.level_display_name,
                    })}
              </dd>
            </div>
            <div>
              <dt>{t('home.donationCredit')}</dt>
              <dd>
                <ExactCredits value={me.data.user.donation_credit} />
              </dd>
            </div>
            <div>
              <dt>{text('user.home.usage')}</dt>
              <dd>
                {me.data.user.usage.total_requests === '0'
                  ? text('user.home.noCalls')
                  : text('user.home.calls', { count: me.data.user.usage.total_requests })}
              </dd>
            </div>
          </dl>
        </>
      )}
    </section>
  );
}

function OnboardingChecklist({
  accountId,
  sessionReady,
}: {
  accountId: string;
  sessionReady: boolean;
}) {
  const { t } = useRegisteredCopy(pageCopyKeys);
  const clientDone = useOnboardingFlag('client');
  const hidden = useOnboardingFlag('hidden');
  const enabled = sessionReady && !hidden;
  const key = useCallerKey(accountId, enabled);
  const models = useNumberedModels(accountId, { page: '1', pageSize: 10 }, enabled);
  const charity = useCharityCatalog(
    accountId,
    {
      page: '1',
      pageSize: 10,
      query: '',
      allowedForMe: 'true',
      allowedLevel: 'all',
      currentlyAvailable: 'all',
      modelType: 'all',
    },
    enabled,
  );
  const keyDone = Boolean(key.data?.metadata);
  const modelDone = Boolean(models.data?.data.length || charity.data?.models.length);
  const complete = keyDone && modelDone && clientDone;
  if (hidden) return null;
  return (
    <section
      className={`nb-panel home-start${complete ? ' home-start--complete' : ''}`}
      aria-labelledby="home-start-title"
    >
      <div className="nb-between">
        <h2 id="home-start-title">
          {complete ? t('user.home.setupDone') : t('user.home.getStarted')}
        </h2>
        {!complete ? (
          <span className="nb-muted nb-small">
            {t('user.home.progress', {
              count: Number(keyDone) + Number(modelDone) + Number(clientDone),
            })}
          </span>
        ) : null}
        {complete ? <Link to="/keys">{t('user.home.apiAccess')}</Link> : null}
      </div>
      {!complete ? (
        <ol className="home-checklist">
          <li data-done={keyDone || undefined}>
            <span className="home-step-dot" aria-hidden="true">
              {keyDone ? '✓' : '1'}
            </span>
            <div>
              <strong>{t('user.home.keyStep')}</strong>
              <p>{t('user.home.keyHelp')}</p>
            </div>
            <Link
              className={`nb-btn nb-btn--sm ${keyDone ? 'nb-btn--ghost' : 'nb-btn--primary'}`}
              to="/keys"
            >
              {keyDone ? t('user.home.view') : t('user.home.create')}
            </Link>
          </li>
          <li data-done={modelDone || undefined}>
            <span className="home-step-dot" aria-hidden="true">
              {modelDone ? '✓' : '2'}
            </span>
            <div>
              <strong>{t('user.home.modelStep')}</strong>
              <p>{t('user.home.modelHelp')}</p>
            </div>
            <span className="nb-inline">
              <Link className="nb-btn nb-btn--secondary nb-btn--sm" to="/charity">
                {t('user.home.charity')}
              </Link>
              <Link className="nb-btn nb-btn--secondary nb-btn--sm" to="/endpoints?quickstart=1">
                {t('user.home.services')}
              </Link>
            </span>
          </li>
          <li data-done={clientDone || undefined}>
            <span className="home-step-dot" aria-hidden="true">
              {clientDone ? '✓' : '3'}
            </span>
            <div>
              <strong>{t('user.home.clientStep')}</strong>
              <ApiAddressCopy />
              <p>{t('user.home.clientHelp')}</p>
            </div>
            <Link className="nb-btn nb-btn--secondary nb-btn--sm" to="/keys#client">
              {t('user.home.clientGuide')}
            </Link>
          </li>
        </ol>
      ) : null}
      <p className="nb-small nb-muted">
        {!complete ? t('user.home.collapseHelp') : null}{' '}
        <button
          className="nb-btn nb-btn--ghost nb-btn--sm"
          type="button"
          onClick={() => markOnboarding('hidden')}
        >
          {t('user.home.hide')}
        </button>
      </p>
    </section>
  );
}

function CheckinCard({
  accountId,
  asset,
  capability,
}: {
  accountId: string;
  capability: HomeAdapters['checkin'];
  asset: CreditAsset;
}) {
  const { t } = useCoreCopy();
  const { t: text } = useRegisteredCopy(pageCopyKeys);
  const queryClient = useQueryClient();
  const [committed, setCommitted] = useState<CommittedCheckin | null>(null);
  const [outcomeUnknown, setOutcomeUnknown] = useState(false);
  const [reconciling, setReconciling] = useState(false);
  const loader = capability.state === 'available' ? capability.load : null;
  const submitter = capability.state === 'available' ? capability.submit : null;
  const status = useQuery({
    queryKey: coreKeys.home(accountId, asset === 'game' ? 'game-checkin' : 'checkin'),
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

  const refreshBothStatusesAndBalances = async () => {
    const ownStatus = status.refetch();
    await Promise.all([
      ownStatus,
      queryClient.invalidateQueries({
        queryKey: coreKeys.home(accountId, asset === 'game' ? 'checkin' : 'game-checkin'),
      }),
      queryClient.invalidateQueries({ queryKey: coreKeys.me(accountId) }),
    ]);
    return ownStatus;
  };

  const refreshAuthority = async () => {
    setReconciling(true);
    try {
      const authority = await refreshBothStatusesAndBalances();
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
      await refreshBothStatusesAndBalances();
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
  const blockedByOtherCheckin = displayedAuthority?.blocked_by_other_checkin ?? false;
  const capReached =
    displayedAuthority !== null &&
    displayedAuthority.balance_cap !== '0' &&
    checkinMilli(displayedAuthority.balance) >= checkinMilli(displayedAuthority.balance_cap);
  return (
    <section className="core-card core-checkin-card">
      <div className="core-card__header">
        <h2>{t(asset === 'game' ? 'home.gameCheckinTitle' : 'home.checkinTitle')}</h2>
        {capability.state === 'available' &&
        !outcomeUnknown &&
        !status.isPending &&
        displayedAuthority ? (
          checkedIn || blockedByOtherCheckin ? (
            <span className={checkedIn ? 'nb-badge nb-badge--ok' : 'nb-badge'}>
              {text(checkedIn ? 'user.home.claimedToday' : 'user.home.availableTomorrow')}
            </span>
          ) : (
            <button
              type="button"
              className="nb-btn nb-btn--primary"
              disabled={
                mutation.isPending || status.isFetching || status.error !== null || capReached
              }
              onClick={() => void submit()}
            >
              {mutation.isPending ? t('common.working') : t('home.checkin.submit')}
            </button>
          )
        ) : null}
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
              className="nb-btn nb-btn--secondary"
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
          <p className="home-checkin-status">
            <span>
              {checkedIn
                ? t('home.checkin.checkedIn')
                : blockedByOtherCheckin
                  ? t('home.checkin.otherChosen')
                  : t('home.checkin.notCheckedIn')}
            </span>
            <span className="nb-muted">
              {' '}
              · <ExactCredits value={displayedAuthority.award_min} />–
              <ExactCredits value={displayedAuthority.award_max} />
            </span>
          </p>
          {blockedByOtherCheckin ? (
            <p className="core-status-message">{t('home.checkin.otherChosenHint')}</p>
          ) : null}
          {displayedAuthority.balance_cap !== '0' ? (
            <p className="core-muted">
              {t('home.checkin.threshold')} <ExactCredits value={displayedAuthority.balance_cap} />
            </p>
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
                  className="nb-btn nb-btn--secondary"
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
        </>
      )}
    </section>
  );
}

function CapabilitySections({
  accountId,
  adapters,
}: {
  accountId: string;
  adapters: HomeAdapters;
}) {
  const { t } = useCoreCopy();
  const queryClient = useQueryClient();
  const gamesLoader = adapters.games.state === 'available' ? adapters.games.load : null;
  const games = useQuery({
    queryKey: coreKeys.home(accountId, 'games'),
    queryFn: ({ signal }) => {
      if (!gamesLoader) throw new CapabilityUnavailableError();
      return accountScopedHomeLoad(queryClient, accountId, gamesLoader, signal);
    },
    enabled: adapters.games.state === 'available',
    retry: false,
  });
  const generalLoader = adapters.checkin.state === 'available' ? adapters.checkin.load : null;
  const gameLoader = adapters.gameCheckin.state === 'available' ? adapters.gameCheckin.load : null;
  const generalStatus = useQuery({
    queryKey: coreKeys.home(accountId, 'checkin'),
    queryFn: ({ signal }) => {
      if (!generalLoader) throw new CapabilityUnavailableError();
      return accountScopedHomeLoad(queryClient, accountId, generalLoader, signal);
    },
    enabled: adapters.checkin.state === 'available',
    retry: false,
  });
  const gameStatus = useQuery({
    queryKey: coreKeys.home(accountId, 'game-checkin'),
    queryFn: ({ signal }) => {
      if (!gameLoader) throw new CapabilityUnavailableError();
      return accountScopedHomeLoad(queryClient, accountId, gameLoader, signal);
    },
    enabled: adapters.gameCheckin.state === 'available',
    retry: false,
  });
  const mutuallyExclusive =
    generalStatus.data?.mutually_exclusive || gameStatus.data?.mutually_exclusive;
  return (
    <>
      {mutuallyExclusive ? (
        <p className="core-checkin-choice-note" role="note">
          {t('home.checkin.exclusiveNotice')}
        </p>
      ) : null}
      <div className="core-checkin-grid">
        <CheckinCard
          key={`general:${accountId}`}
          accountId={accountId}
          asset="general"
          capability={adapters.checkin}
        />
        <CheckinCard
          key={`game:${accountId}`}
          accountId={accountId}
          asset="game"
          capability={adapters.gameCheckin}
        />
      </div>

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
    </>
  );
}

export function HomeDashboard({
  user,
  adapters = productionHomeAdapters,
  sessionReady = true,
}: {
  user: UserProfile;
  adapters?: HomeAdapters;
  sessionReady?: boolean;
}) {
  const { t } = useCoreCopy();
  return (
    <div className="page core-page core-stack home-page">
      <PageHeader
        icon="home"
        title={t('home.title', { name: user.guild_nick || user.username })}
        description={t('home.description')}
      />
      <div className="home-hero">
        <OnboardingChecklist key={user.id} accountId={user.id} sessionReady={sessionReady} />
        <EconomyCard accountId={user.id} />
      </div>
      <CapabilitySections accountId={user.id} adapters={adapters} />
      <HomeAnnouncements
        accountId={user.id}
        language={user.lang}
        capability={adapters.announcements}
        sessionReady={sessionReady}
      />
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
      {(user) => <HomeDashboard key={user.id} user={user} sessionReady />}
    </CoreProfileGate>
  );
}
