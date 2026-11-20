import {
  CancelledError,
  useMutation,
  useQuery,
  useQueryClient,
  type QueryClient,
} from '@tanstack/react-query';
import { useEffect, useRef, useState } from 'react';
import { Link } from 'react-router';
import { useRegisteredCopy } from '@shared/i18n/useRegisteredCopy';
import { ApiAddressCopy, markOnboarding, useOnboardingFlag } from '../features/core/onboarding';
import { useNumberedModels } from '../features/core/modelNumberedQueries';
import { DEFAULT_CHARITY_CATALOG_FILTERS, useCharityCatalog } from '../features/economy/catalog';
import { Icon } from '@shared/components/Icon';
import { useWhaleHavenOpen } from '../games/common/useWhaleHavenOpen';
import { PageHeader } from '@shared/components/States';
import { StatusBadge } from '@shared/components/ui';
import { Stepper, type StepState, type StepperStep } from '@shared/components/ui/Stepper';
import { isNotFoundError, isUnauthorized } from '@shared/query/http';
import {
  CoreErrorPanel,
  CoreLoading,
  CoreProfileGate,
  CoreUnavailable,
  ExactCredits,
} from '../features/core/components';
import { useCoreCopy } from '../features/core/copy';
import { creditHistoryKeys } from '../features/credits/queries';
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
  'user.games.copy.center.whalehaven.title': 'user.games.copy.center.whalehaven.title',
  'user.home.whaleVisit': 'user.home.whaleVisit',
  'user.home.dailyCheckins': 'user.home.dailyCheckins',
  'user.home.balanceLimitReached': 'user.home.balanceLimitReached',
  'user.home.checkinReward': 'user.home.checkinReward',
  'user.home.apiAccess': 'user.home.apiAccess',
  'user.home.calls': 'user.home.calls',
  'user.home.charity': 'user.home.charity',
  'user.home.claimedToday': 'user.home.claimedToday',
  'user.home.availableTomorrow': 'user.home.availableTomorrow',
  'user.home.clientGuide': 'user.home.clientGuide',
  'user.home.clientHelp': 'user.home.clientHelp',
  'user.home.clientStep': 'user.home.clientStep',
  'user.home.create': 'user.home.create',
  'user.home.getStarted': 'user.home.getStarted',
  'user.home.hide': 'user.home.hide',
  'user.home.showGuide': 'user.home.showGuide',
  'user.home.introAddress': 'user.home.introAddress',
  'user.home.introAddressBody': 'user.home.introAddressBody',
  'user.home.introCharity': 'user.home.introCharity',
  'user.home.introCharityBody': 'user.home.introCharityBody',
  'user.home.introGames': 'user.home.introGames',
  'user.home.introGamesBody': 'user.home.introGamesBody',
  'user.home.keyHelp': 'user.home.keyHelp',
  'user.home.keyStep': 'user.home.keyStep',
  'user.home.keyStepDone': 'user.home.keyStepDone',
  'user.home.modelHelp': 'user.home.modelHelp',
  'user.home.modelStep': 'user.home.modelStep',
  'user.home.noCalls': 'user.home.noCalls',
  'user.home.progress': 'user.home.progress',
  'user.home.services': 'user.home.services',
  'user.home.setupDone': 'user.home.setupDone',
  'user.home.stepDone': 'user.home.stepDone',
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

function WhaleHavenEntry({ enabled }: { enabled: boolean }) {
  const open = useWhaleHavenOpen(enabled);
  const { t } = useRegisteredCopy(pageCopyKeys);
  if (!open) return null;
  return (
    <Link
      className="nb-panel home-whale-entry"
      to="/games/whale-haven"
      aria-labelledby="home-whale-title"
    >
      <img
        src="/assets/whale-haven/sprites/whale-home.webp"
        alt=""
        width={60}
        height={64}
        decoding="async"
      />
      <span className="home-whale-entry__copy">
        <strong id="home-whale-title">{t('user.games.copy.center.whalehaven.title')}</strong>
        <span>{t('user.home.whaleVisit')}</span>
      </span>
      <Icon name="chevron-right" />
    </Link>
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
          <dl className="nb-facts nb-facts--inline home-wallet__facts nb-small">
            <div>
              <dt>{t('home.level')}</dt>
              <dd>
                {new RegExp(`^Lv\\.?\\s*${me.data.user.effective_level}$`, 'i').test(
                  me.data.user.level_display_name,
                )
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

function useHomeOnboarding(accountId: string, sessionReady: boolean) {
  const clientDone = useOnboardingFlag('client');
  const hidden = useOnboardingFlag('hidden');
  const enabled = sessionReady && !hidden;
  const key = useCallerKey(accountId, enabled);
  const firstPage = { page: '1', pageSize: 10 } as const;
  const models = useNumberedModels(accountId, firstPage, enabled, {
    connection_state: 'available',
  });
  const charity = useCharityCatalog(
    accountId,
    {
      ...DEFAULT_CHARITY_CATALOG_FILTERS,
      ...firstPage,
    },
    enabled && (models.isError || (models.isSuccess && models.data.data.length === 0)),
  );
  const keyDone = Boolean(key.data?.metadata);
  const modelDone = Boolean(models.data?.data.length || charity.data?.models.length);
  const complete = keyDone && modelDone && clientDone;
  return { hidden, keyDone, modelDone, clientDone, complete };
}

function OnboardingChecklist({
  progress,
  focusOnShow,
}: {
  progress: ReturnType<typeof useHomeOnboarding>;
  focusOnShow: boolean;
}) {
  const { t } = useRegisteredCopy(pageCopyKeys);
  const { keyDone, modelDone, clientDone, complete } = progress;
  const titleRef = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    if (focusOnShow) titleRef.current?.focus();
  }, [focusOnShow]);
  const done = [keyDone, modelDone, clientDone];
  // The first unfinished step is the suggested next one; later steps stay usable.
  const stateOf = (index: number): StepState =>
    done[index] ? 'done' : index === done.indexOf(false) ? 'current' : 'todo';
  const button = (index: number) =>
    `nb-btn nb-btn--sm ${stateOf(index) === 'current' ? 'nb-btn--primary' : 'nb-btn--secondary'}`;
  const quiet = 'nb-btn nb-btn--ghost nb-btn--sm';
  const modelLinks = (primary: string, secondary: string) => (
    <>
      <Link className={primary} to="/charity">
        {t('user.home.charity')}
      </Link>
      <Link className={secondary} to="/endpoints?quickstart=1">
        {t('user.home.services')}
      </Link>
    </>
  );
  const guide = (className: string) => (
    <Link className={className} to="/keys#client">
      {t('user.home.clientGuide')}
    </Link>
  );
  const steps: StepperStep[] = [
    {
      id: 'key',
      state: stateOf(0),
      title: t(keyDone ? 'user.home.keyStepDone' : 'user.home.keyStep'),
      description: keyDone ? undefined : t('user.home.keyHelp'),
      action: (
        <Link className={button(0)} to="/keys">
          {t('user.home.create')}
        </Link>
      ),
      doneAction: (
        <Link className={quiet} to="/keys">
          {t('user.home.view')}
        </Link>
      ),
    },
    {
      id: 'model',
      state: stateOf(1),
      title: t('user.home.modelStep'),
      description: modelDone ? undefined : t('user.home.modelHelp'),
      action: modelLinks(button(1), 'nb-btn nb-btn--secondary nb-btn--sm'),
      doneAction: modelLinks(quiet, quiet),
    },
    {
      id: 'client',
      state: stateOf(2),
      title: t('user.home.clientStep'),
      description: clientDone ? undefined : t('user.home.clientHelp'),
      children: clientDone ? null : <ApiAddressCopy />,
      action: guide(button(2)),
      doneAction: guide(quiet),
    },
  ];
  const hide = (
    <button className={quiet} type="button" onClick={() => markOnboarding('hidden')}>
      {t('user.home.hide')}
    </button>
  );
  return (
    <section
      className={`nb-panel home-start${complete ? ' home-start--complete' : ''}`}
      aria-labelledby="home-start-title"
    >
      <div className="home-start__head">
        <h2 id="home-start-title" ref={titleRef} tabIndex={-1}>
          {complete ? t('user.home.setupDone') : t('user.home.getStarted')}
        </h2>
        <div className="home-start__side">
          {complete ? (
            <Link to="/keys">{t('user.home.apiAccess')}</Link>
          ) : (
            <span className="nb-muted nb-small">
              {t('user.home.progress', {
                count: Number(keyDone) + Number(modelDone) + Number(clientDone),
              })}
            </span>
          )}
          {hide}
        </div>
      </div>
      {!complete ? (
        <Stepper
          className="home-checklist"
          label={t('user.home.getStarted')}
          doneLabel={t('user.home.stepDone')}
          steps={steps}
        />
      ) : null}
    </section>
  );
}

function useCheckinStatus(
  accountId: string,
  asset: CreditAsset,
  capability: HomeAdapters['checkin'],
) {
  const queryClient = useQueryClient();
  const loader = capability.state === 'available' ? capability.load : null;
  return useQuery({
    queryKey: coreKeys.home(accountId, asset === 'game' ? 'game-checkin' : 'checkin'),
    queryFn: ({ signal }) => {
      if (!loader) throw new CapabilityUnavailableError();
      return accountScopedHomeLoad(queryClient, accountId, loader, signal);
    },
    enabled: capability.state === 'available',
    retry: false,
  });
}

function CheckinCard({
  accountId,
  asset,
  capability,
  status,
}: {
  accountId: string;
  capability: HomeAdapters['checkin'];
  asset: CreditAsset;
  status: ReturnType<typeof useCheckinStatus>;
}) {
  const { t } = useCoreCopy();
  const { t: text } = useRegisteredCopy(pageCopyKeys);
  const queryClient = useQueryClient();
  const [committed, setCommitted] = useState<CommittedCheckin | null>(null);
  const [outcomeUnknown, setOutcomeUnknown] = useState(false);
  const [reconciling, setReconciling] = useState(false);
  const submitter = capability.state === 'available' ? capability.submit : null;
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
      queryClient.invalidateQueries({ queryKey: creditHistoryKeys.account(accountId) }),
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
    <section className="core-card core-checkin-card" aria-labelledby={'home-checkin-' + asset}>
      <div className="core-card__header">
        <h3 id={'home-checkin-' + asset}>
          {t(asset === 'game' ? 'home.gameCheckinTitle' : 'home.checkinTitle')}
        </h3>
        {capability.state === 'available' &&
        !outcomeUnknown &&
        !status.isPending &&
        displayedAuthority ? (
          checkedIn || blockedByOtherCheckin ? (
            <StatusBadge tone={checkedIn ? 'ok' : 'neutral'}>
              {text(checkedIn ? 'user.home.claimedToday' : 'user.home.availableTomorrow')}
            </StatusBadge>
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
                  : capReached
                    ? text('user.home.balanceLimitReached')
                    : t('home.checkin.notCheckedIn')}
            </span>
            <span className="nb-muted">
              {text('user.home.checkinReward')}{' '}
              <ExactCredits value={displayedAuthority.award_min} />–
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

/** Both daily check-ins, shown beside the wallet they add to. */
function CheckinSection({ accountId, adapters }: { accountId: string; adapters: HomeAdapters }) {
  const { t } = useCoreCopy();
  const { t: text } = useRegisteredCopy(pageCopyKeys);
  const generalStatus = useCheckinStatus(accountId, 'general', adapters.checkin);
  const gameStatus = useCheckinStatus(accountId, 'game', adapters.gameCheckin);
  const bothKnown = generalStatus.isSuccess && gameStatus.isSuccess;
  const showGeneral = !bothKnown || generalStatus.data.enabled;
  const showGame = !bothKnown || gameStatus.data.enabled;
  if (!showGeneral && !showGame) return null;
  const mutuallyExclusive =
    generalStatus.data?.mutually_exclusive || gameStatus.data?.mutually_exclusive;
  return (
    <section className="nb-panel home-checkins" aria-labelledby="home-checkins-title">
      <h2 id="home-checkins-title">{text('user.home.dailyCheckins')}</h2>
      {showGeneral && showGame && mutuallyExclusive ? (
        <p className="core-checkin-choice-note" role="note">
          {t('home.checkin.exclusiveNotice')}
        </p>
      ) : null}
      <div className="core-checkin-grid">
        {showGeneral && (
          <CheckinCard
            key={`general:${accountId}`}
            accountId={accountId}
            asset="general"
            capability={adapters.checkin}
            status={generalStatus}
          />
        )}
        {showGame && (
          <CheckinCard
            key={`game:${accountId}`}
            accountId={accountId}
            asset="game"
            capability={adapters.gameCheckin}
            status={gameStatus}
          />
        )}
      </div>
    </section>
  );
}

function GamesSection({ accountId, adapters }: { accountId: string; adapters: HomeAdapters }) {
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
  return (
    <>
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
                  <StatusBadge tone={item.kind === 'continue' ? 'accent' : 'neutral'}>
                    {item.kind === 'continue' ? t('home.continue') : t('home.view')}
                  </StatusBadge>
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
  const { t: text } = useRegisteredCopy(pageCopyKeys);
  const onboarding = useHomeOnboarding(user.id, sessionReady);
  const [guideRequested, setGuideRequested] = useState(false);
  const guideFirst = !onboarding.complete || guideRequested;
  const guide = onboarding.hidden ? null : (
    <OnboardingChecklist progress={onboarding} focusOnShow={guideRequested} />
  );
  return (
    <div className="page core-page core-stack home-page">
      <div className="home-welcome">
        <PageHeader
          icon="home"
          title={t('home.title', { name: user.guild_nick || user.username })}
          actions={
            onboarding.hidden ? (
              <button
                type="button"
                className="nb-btn nb-btn--ghost nb-btn--sm"
                onClick={() => {
                  setGuideRequested(true);
                  markOnboarding('hidden', false);
                }}
              >
                {text('user.home.showGuide')}
              </button>
            ) : null
          }
        />
        <WhaleHavenEntry enabled={sessionReady} />
      </div>
      {guideFirst ? guide : null}
      <div className="home-daily">
        <EconomyCard accountId={user.id} />
        <CheckinSection accountId={user.id} adapters={adapters} />
      </div>
      <HomeAnnouncements
        accountId={user.id}
        language={user.lang}
        capability={adapters.announcements}
        sessionReady={sessionReady}
      />
      <GamesSection accountId={user.id} adapters={adapters} />
      {guideFirst ? null : guide}
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
