import { useCallback, useEffect, useId, useReducer, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import { useQueryClient } from '@tanstack/react-query';
import { ApiError, isApiError } from '@shared/query/http';
import { Card, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { formatCreditsFromMilli } from '@shared/utils/formatNumber';
import { useUserSession } from '../../data';
import { FishingArtwork } from './FishingArtwork';
import {
  isAffordable,
  fishingKeys,
  isFishingGameSummary,
  safeAvatarUrl,
  useAcknowledgeFishing,
  useFishingConfig,
  useFishingLeaderboard,
  useFishingState,
  useSettleFishing,
  useStartFishing,
  useUpdateGameProfilePublic,
} from './client';
import {
  appendDeferredResult,
  FISHING_PHASE_DURATIONS_MS,
  fishingFlowReducer,
  initialFishingFlow,
  isFishingBusy,
} from './stateMachine';
import { gameText, itemName } from './text';
import type { BaitId, FishingBait, FishingFlowState, FishingResult, FishingState, Leaderboard, LeaderboardEntry, PendingRound } from './types';
import './fishing.css';

function amountLabel(value: string): { display: string; exact: string } {
  const formatted = formatCreditsFromMilli(value);
  return { display: formatted.display, exact: formatted.exact };
}

const REDUCED_MOTION_QUERY = '(prefers-reduced-motion: reduce)';

function prefersReducedMotion(): boolean {
  return typeof window !== 'undefined'
    && typeof window.matchMedia === 'function'
    && window.matchMedia(REDUCED_MOTION_QUERY).matches;
}

function CreditAmount({ value, label }: { value: string; label?: string }) {
  const formatted = amountLabel(value);
  return (
    <span title={`${formatted.exact} milli-credits`}>
      {label ? `${label}: ` : ''}{formatted.display}
    </span>
  );
}

function createIdempotencyKey(): string {
  const cryptoSource = globalThis.crypto;
  if (typeof cryptoSource?.randomUUID === 'function') return cryptoSource.randomUUID();
  if (typeof cryptoSource?.getRandomValues !== 'function') {
    throw new Error('Secure random UUID generation is unavailable.');
  }
  const bytes = new Uint8Array(16);
  cryptoSource.getRandomValues(bytes);
  // RFC 4122 version 4 / variant 1, using only the browser CSPRNG.
  bytes[6] = (bytes[6] & 0x0f) | 0x40;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  const hex = Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('');
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

function isPublicLeaderboardEntry(entry: LeaderboardEntry): boolean {
  return Boolean(entry.displayName);
}

function Avatar({ entry, t, onError }: { entry: LeaderboardEntry; t: TFunction; onError: () => void }) {
  const fallback = gameText(t, 'games.fishing.leaderboard.avatarFallback', 'Avatar unavailable');
  const src = safeAvatarUrl(entry.avatarUrl);
  if (!src) return <span className="fishing-avatar-fallback" role="img" aria-label={fallback}>?</span>;
  return (
    <img
      className="fishing-avatar"
      src={src}
      alt={gameText(t, 'games.fishing.leaderboard.avatarAlt', 'Avatar for {{name}}', { name: entry.displayName ?? '' })}
      loading="lazy"
      width="32"
      height="32"
      referrerPolicy="no-referrer"
      onError={onError}
    />
  );
}

function LeaderboardRow({ entry, board, t }: { entry: LeaderboardEntry; board: 'single' | 'total'; t: TFunction }) {
  const [avatarFailed, setAvatarFailed] = useState(false);
  const publicEntry = isPublicLeaderboardEntry(entry);
  const displayName = publicEntry
    ? entry.displayName
    : gameText(t, 'games.fishing.leaderboard.anonymous', 'Anonymous angler');
  const safeEntry = avatarFailed ? { ...entry, avatarUrl: undefined } : entry;
  const score = board === 'single'
    ? (entry.sizeCm === undefined ? '—' : `${entry.sizeCm} cm`)
    : <CreditAmount value={entry.totalCredits ?? '0'} />;
  return (
    <tr className={entry.isMe ? 'is-me' : undefined}>
      <td className="fishing-rank">{entry.rank}</td>
      <td>
        <span className="fishing-player">
          {publicEntry ? <Avatar entry={safeEntry} t={t} onError={() => setAvatarFailed(true)} /> : null}
          <span className="fishing-player__name">{displayName}</span>
          {publicEntry && entry.level4Badge ? (
            <span className="fishing-badge" title={gameText(t, 'games.fishing.leaderboard.level4Hint', 'Level 4 visual badge')}>
              {gameText(t, 'games.fishing.leaderboard.level4', 'L4')}
            </span>
          ) : null}
        </span>
      </td>
      <td>{board === 'single' ? itemName(t, entry.speciesKey ?? 'unknown') : gameText(t, 'games.fishing.leaderboard.totalScore', 'Total payout')}</td>
      <td>{score}</td>
      <td>{entry.isMe ? gameText(t, 'games.fishing.leaderboard.me', 'You') : null}</td>
    </tr>
  );
}

function hideOwnLeaderboardIdentity(entry: LeaderboardEntry): LeaderboardEntry {
  return {
    ...entry,
    displayName: undefined,
    avatarUrl: undefined,
    level4Badge: undefined,
  };
}

function projectLeaderboardPrivacy(data: Leaderboard | undefined, profilePublic: boolean): Leaderboard | undefined {
  if (!data || profilePublic) return data;
  return {
    ...data,
    entries: data.entries.map((entry) => (entry.isMe ? hideOwnLeaderboardIdentity(entry) : entry)),
    // `me` is always the caller's row, even if a defensive server response
    // omitted its is_me marker. Keep it identity-free until public is confirmed.
    me: data.me ? hideOwnLeaderboardIdentity(data.me) : null,
  };
}

function LeaderboardCard({ board, data, error, isPending, onRetry }: {
  board: 'single' | 'total';
  data: Leaderboard | undefined;
  error: unknown;
  isPending: boolean;
  onRetry: () => void;
}) {
  const { t } = useTranslation();
  const title = board === 'single'
    ? gameText(t, 'games.fishing.leaderboard.singleTitle', 'Best catch')
    : gameText(t, 'games.fishing.leaderboard.totalTitle', 'Recent total catch');
  const description = board === 'single'
    ? gameText(t, 'games.fishing.leaderboard.singleDescription', 'Your longest fish stays on this board.')
    : gameText(t, 'games.fishing.leaderboard.totalDescription', 'Committed payouts from the last 30 days.')
  return (
    <Card className="fishing-leaderboard">
      <div className="fishing-leaderboard__heading">
        <div>
          <h2>{title}</h2>
          <p className="fishing-card__hint">{description}</p>
        </div>
        <span className="fishing-card__hint">Top 20 + {gameText(t, 'games.fishing.leaderboard.mine', 'mine')}</span>
      </div>
      {isPending ? <LoadingState label={gameText(t, 'games.fishing.leaderboard.loading', 'Loading leaderboard…')} /> : null}
      {error ? <ErrorState error={error} onRetry={onRetry} /> : null}
      {!isPending && !error && data && data.entries.length === 0 && !data.me ? (
        <p className="fishing-empty">{gameText(t, 'games.fishing.leaderboard.empty', 'No settled catches yet.')}</p>
      ) : null}
      {!isPending && !error && data && (data.entries.length > 0 || data.me) ? (
        <div className="fishing-leaderboard__table-wrap">
          <table className="fishing-leaderboard__table">
            <caption className="sr-only">{title}</caption>
            <thead>
              <tr>
                <th scope="col">{gameText(t, 'games.fishing.leaderboard.rank', 'Rank')}</th>
                <th scope="col">{gameText(t, 'games.fishing.leaderboard.angler', 'Angler')}</th>
                <th scope="col">{board === 'single' ? gameText(t, 'games.fishing.leaderboard.catch', 'Catch') : gameText(t, 'games.fishing.leaderboard.kind', 'Score')}</th>
                <th scope="col">{board === 'single' ? gameText(t, 'games.fishing.leaderboard.size', 'Size') : gameText(t, 'games.fishing.leaderboard.payout', 'Payout')}</th>
                <th scope="col"><span className="sr-only">{gameText(t, 'games.fishing.leaderboard.you', 'You')}</span></th>
              </tr>
            </thead>
            <tbody>
              {data.entries.map((entry) => <LeaderboardRow key={`${entry.rank}-${entry.isMe ? 'me' : 'row'}`} entry={entry} board={board} t={t} />)}
              {data.me && !data.entries.some((entry) => entry.isMe) ? (
                <LeaderboardRow key={`me-${data.me.rank}`} entry={data.me} board={board} t={t} />
              ) : null}
            </tbody>
          </table>
        </div>
      ) : null}
      {!isPending && !error && data?.me && !data.entries.some((entry) => entry.isMe) ? (
        <p className="fishing-card__hint">
          {gameText(t, 'games.fishing.leaderboard.yourRank', 'Your rank: #{{rank}}', { rank: data.me.rank })}
        </p>
      ) : null}
    </Card>
  );
}

function resultKind(result: FishingResult): 'fish' | 'junk' | 'treasure' {
  if (result.isTreasure) return 'treasure';
  if (result.isJunk) return 'junk';
  return 'fish';
}

function ResultCard({ result, source, hasMore, onAck, ackPending, ackDisabled, balanceKnown, balance }: {
  result: FishingResult;
  source: 'settled' | 'recovered';
  hasMore: boolean;
  onAck: (button: HTMLButtonElement) => void;
  ackPending: boolean;
  ackDisabled: boolean;
  balanceKnown: boolean;
  balance: string;
}) {
  const { t } = useTranslation();
  const titleId = useId();
  const kind = resultKind(result);
  const name = itemName(t, result.speciesKey);
  const title = kind === 'fish'
    ? gameText(t, 'games.fishing.result.fishTitle', 'Catch confirmed')
    : kind === 'treasure'
      ? gameText(t, 'games.fishing.result.treasureTitle', 'Treasure found')
      : gameText(t, 'games.fishing.result.junkTitle', 'A quiet catch');
  const isZero = result.creditsWon === '0';
  return (
    <section
      role="region"
      aria-labelledby={titleId}
      className={`card fishing-result fishing-result--${kind}`}
      aria-live="polite"
      data-round-id={result.roundId}
      data-species-key={result.speciesKey}
      data-credits-won={result.creditsWon}
    >
      <div className="fishing-result__heading">
        <h2 id={titleId}>{title}</h2>
        <span className="fishing-card__hint">
          {source === 'recovered'
            ? gameText(t, 'games.fishing.result.recovered', 'Recovered from the server')
            : gameText(t, 'games.fishing.result.serverConfirmed', 'Server confirmed')}
        </span>
      </div>
      <div className="fishing-result__art">
        <FishingArtwork itemKey={result.speciesKey} label={name} />
        <div>
          <p className="fishing-result__hint">
            {kind === 'fish'
              ? gameText(t, 'games.fishing.result.fishDescription', '{{name}} · {{tier}} tier', { name, tier: result.tier })
              : kind === 'treasure'
                ? gameText(t, 'games.fishing.result.treasureDescription', '{{name}}', { name })
                : gameText(t, 'games.fishing.result.junkDescription', '{{name}} · no payout', { name })}
          </p>
          <div className="fishing-result__meta">
            {kind === 'fish' ? <span>{gameText(t, 'games.fishing.result.size', 'Size')}: {result.sizeCm} cm</span> : null}
            {result.meter ? <span>{gameText(t, 'games.fishing.result.meter', 'Meter catch')}</span> : null}
          </div>
          <p className={`fishing-result__amount${isZero ? ' fishing-result__amount--zero' : ''}`}>
            <CreditAmount
              value={result.creditsWon}
              label={gameText(t, 'games.fishing.result.won', 'Won')}
            />
          </p>
          <p className="fishing-card__hint" data-testid="fishing-result-balance">
            {gameText(t, 'games.fishing.result.balance', 'Available credits')}: {balanceKnown
              ? <CreditAmount value={balance} />
              : gameText(t, 'games.fishing.balanceUnknown', 'Unknown until the server confirms the balance')}
          </p>
        </div>
      </div>
      <div className="fishing-result__actions">
        <button
          type="button"
          className="btn btn-primary"
          data-testid="fishing-result-ack"
          onClick={(event) => onAck(event.currentTarget)}
          disabled={ackPending || ackDisabled}
        >
          {ackPending ? gameText(t, 'games.fishing.result.ackWorking', 'Confirming…') : gameText(t, 'games.fishing.result.ack', 'Mark as viewed')}
        </button>
        {hasMore ? <span className="fishing-card__hint">{gameText(t, 'games.fishing.result.more', 'Another result is ready after this one.')}</span> : null}
      </div>
    </section>
  );
}

function FishingStage({ flow, pending, onRetry, retryDisabled, canRetry, l4Active }: {
  flow: FishingFlowState;
  pending: PendingRound | null;
  onRetry: () => void;
  retryDisabled: boolean;
  canRetry: boolean;
  l4Active: boolean;
}) {
  const { t } = useTranslation();
  const phase = flow.phase;
  const phaseDefaults: Record<Exclude<typeof phase, 'error'>, string> = {
    idle: 'Choose bait to cast a line.',
    casting: 'Casting the line…',
    waiting: 'Waiting for a bite…',
    reeling: 'Reeling in…',
    settling: 'Confirming the server result…',
    result: 'Catch ready to review.',
  };
  const defaultText = phase === 'error'
    ? canRetry
      ? gameText(t, 'games.fishing.phase.error', 'The server has not confirmed this round. Retry settlement when ready.')
      : gameText(t, 'games.fishing.phase.startError', 'No paid round was created. Choose bait to try again.')
    : gameText(t, `games.fishing.phase.${phase}`, phaseDefaults[phase]);
  return (
    <section className={`fishing-card fishing-stage${l4Active ? ' fishing-stage--l4' : ''}`} data-phase={phase} aria-labelledby="fishing-stage-title">
      <div className="fishing-stage__heading">
                <h2 id="fishing-stage-title" tabIndex={-1}>{gameText(t, 'games.fishing.stage.title', 'Pond')}</h2>
        {pending ? <span className="fishing-card__hint">{gameText(t, 'games.fishing.stage.pending', 'Round pending')}</span> : null}
      </div>
      <div className="fishing-pond" aria-hidden="true">
        <span className="fishing-pond__rod" />
        <span className="fishing-pond__line" />
        <span className="fishing-pond__float" />
        <p className="fishing-pond__status">{defaultText}</p>
      </div>
      <p className="fishing-status-line" role="status" aria-live="polite">{defaultText}</p>
      {pending ? (
        <p className="fishing-card__hint">
          {gameText(t, 'games.fishing.stage.autoSettle', 'The server will safely recover this paid round if the page closes.')}
        </p>
      ) : null}
      {phase === 'error' && canRetry ? (
        <button type="button" className="btn btn-secondary" onClick={onRetry} disabled={retryDisabled}>
          {retryDisabled ? gameText(t, 'games.fishing.stage.retryWorking', 'Retrying…') : gameText(t, 'games.fishing.stage.retry', 'Retry settlement')}
        </button>
      ) : null}
    </section>
  );
}

function baitName(t: TFunction, bait: BaitId): string {
  return gameText(t, `games.fishing.baits.${bait}`, bait === 'worm' ? 'Worm' : bait === 'lure' ? 'Lure' : 'Premium lure');
}

function errorReason(t: TFunction, error: unknown): string {
  if (!isApiError(error)) return gameText(t, 'games.fishing.errors.unavailable', 'Unavailable right now');
  switch (error.code) {
    case 'insufficient_credits': return gameText(t, 'games.fishing.errors.insufficient', 'Not enough available credits');
    case 'pending_round': return gameText(t, 'games.fishing.errors.pending', 'Finish the pending round first');
    case 'feature_disabled': return gameText(t, 'games.fishing.errors.disabled', 'Fishing is currently closed');
    case 'rate_limited': return gameText(t, 'games.fishing.errors.rateLimited', 'Too many casts; try again later');
    default: return error.message || gameText(t, 'games.fishing.errors.unavailable', 'Unavailable right now');
  }
}

interface StartAttempt {
  readonly token: number;
  readonly presentationGeneration: number;
  readonly bait: BaitId;
  readonly baselinePendingRoundId: string | null;
  readonly baselineResultId: string | null;
  readonly writeToken: number;
}

export function FishingGame() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const session = useUserSession();
  const config = useFishingConfig(true);
  const state = useFishingState(Boolean(config.data));
  const single = useFishingLeaderboard('single', Boolean(config.data));
  const total = useFishingLeaderboard('total', Boolean(config.data));
  const start = useStartFishing();
  const settle = useSettleFishing();
  const ack = useAcknowledgeFishing();
  const profile = useUpdateGameProfilePublic();
  const [flow, dispatch] = useReducer(fishingFlowReducer, initialFishingFlow);
  // ACK callbacks may resolve after a newer presentation has started. Keep a
  // current-flow ref for the async callback and a synchronous generation
  // guard for races before React commits the next reducer update.
  const flowRef = useRef(flow);
  const presentationGeneration = useRef(0);
  const acknowledgingRound = useRef<string | null>(null);
  useEffect(() => {
    flowRef.current = flow;
  }, [flow]);
  const [selectedBait, setSelectedBait] = useState<BaitId>('worm');
  const timers = useRef<number[]>([]);
  const animatedRound = useRef<string | null>(null);
  const activePendingRound = useRef<PendingRound | null>(null);
  const acknowledgedRounds = useRef(new Set<string>());
  const deferredResults = useRef<FishingResult[]>([]);
  const [acknowledgedRoundIds, setAcknowledgedRoundIds] = useState<ReadonlySet<string>>(new Set());
  const [deferredResultList, setDeferredResultList] = useState<readonly FishingResult[]>([]);
  const settlingRound = useRef<string | null>(null);
  const settleGeneration = useRef(0);
  const activeSettlement = useRef<{ readonly generation: number; readonly pending: PendingRound; readonly writeToken: number } | null>(null);
  const retryPendingRound = useRef<PendingRound | null>(null);
  const [retryPendingRoundState, setRetryPendingRoundState] = useState<PendingRound | null>(null);
  const [invalidSettlementRound, setInvalidSettlementRound] = useState<string | null>(null);
  const [invalidSettlementContext, setInvalidSettlementContext] = useState<PendingRound | null>(null);
  const [settledResultIds, setSettledResultIds] = useState<ReadonlySet<string>>(new Set());
  const [profileReconciling, setProfileReconciling] = useState(false);
  // /api/games is the only balance authority.  React Query intentionally keeps
  // the last successful data snapshot after a failed refetch, so retain an
  // explicit local unknown state instead of treating that snapshot as current.
  const [gamesAuthorityUnknown, setGamesAuthorityUnknown] = useState(false);
  const authorityRefreshToken = useRef(0);
  const [fishingStateAuthorityUnknown, setFishingStateAuthorityUnknown] = useState(false);
  const fishingStateRefreshToken = useRef(0);
  const writeToken = useRef(0);
  const uncertainWriteTokens = useRef(new Set<number>());
  const [uncertainWriteCount, setUncertainWriteCount] = useState(0);
  const startAttemptToken = useRef(0);
  const activeStartAttempt = useRef<StartAttempt | null>(null);
  const gamesAuthorityKnownRef = useRef(false);
  const fishingStateKnownRef = useRef(false);
  const ackFocusRequested = useRef(false);
  const castButtonRef = useRef<HTMLButtonElement | null>(null);
  const MAX_ACKNOWLEDGED_ROUNDS = 64;

  const rememberAcknowledgedRound = useCallback((roundId: string) => {
    acknowledgedRounds.current.add(roundId);
    while (acknowledgedRounds.current.size > MAX_ACKNOWLEDGED_ROUNDS) {
      const oldest = acknowledgedRounds.current.values().next().value;
      if (oldest === undefined) break;
      acknowledgedRounds.current.delete(oldest);
    }
    setAcknowledgedRoundIds(new Set(acknowledgedRounds.current));
  }, []);

  const game = config.data?.games.find(isFishingGameSummary);
  const baits = game?.params.baits ?? [];
  const selected = baits.find((bait) => bait.id === selectedBait);
  const refetchFishingState = state.refetch;
  const markWriteUncertain = useCallback((): number => {
    const token = writeToken.current + 1;
    writeToken.current = token;
    uncertainWriteTokens.current.add(token);
    setUncertainWriteCount(uncertainWriteTokens.current.size);
    return token;
  }, []);

  const clearSettlementRetry = useCallback(() => {
    retryPendingRound.current = null;
    setRetryPendingRoundState(null);
    setInvalidSettlementRound(null);
    setInvalidSettlementContext(null);
  }, []);

  const refetchFishingStateAuthority = useCallback(async (writeToClear?: number): Promise<boolean> => {
    const token = fishingStateRefreshToken.current + 1;
    fishingStateRefreshToken.current = token;
    // A read may overlap a newer write (for example, settlement state
    // reconciliation can still be in flight when an ACK is sent).  Only the
    // writes visible at this read's start may be cleared by its success.  A
    // later write token must remain uncertain until a read started after it
    // succeeds, even if this older read happens to finish last.
    const writeWatermark = writeToken.current;
    setFishingStateAuthorityUnknown(true);
    try {
      const refreshed = await refetchFishingState();
      const success = refreshed.isSuccess && !refreshed.isError && Boolean(refreshed.data);
      if (fishingStateRefreshToken.current === token) {
        setFishingStateAuthorityUnknown(!success);
        if (success) {
          // Reconcile only tokens no newer than the read's start watermark.
          // `writeToClear` is an explicit response/read pairing and is safe
          // to clear even if a caller passes it after the watermark capture.
          let changed = false;
          for (const pendingToken of uncertainWriteTokens.current) {
            if (pendingToken <= writeWatermark || pendingToken === writeToClear) {
              uncertainWriteTokens.current.delete(pendingToken);
              changed = true;
            }
          }
          if (changed) {
            setUncertainWriteCount(uncertainWriteTokens.current.size);
          }
        }
      }
      return success;
    } catch {
      if (fishingStateRefreshToken.current === token) {
        setFishingStateAuthorityUnknown(true);
      }
      return false;
    }
  }, [refetchFishingState]);

  const fishingStateKnown = Boolean(
    state.data
    && !fishingStateAuthorityUnknown
    && !state.isPending
    && !state.isFetching
    && !state.isError,
  );
  const hasPending = Boolean(
    !fishingStateKnown
    || state.data?.pendingRound
    || (retryPendingRoundState && (!fishingStateKnown || state.data?.pendingRound))
    || isFishingBusy(flow.phase)
    || uncertainWriteCount > 0,
  );
  const gamesAuthorityKnown = Boolean(
    config.data
    && !gamesAuthorityUnknown
    && !config.isPending
    && !config.isFetching
    && !config.isError,
  );
  const featureEnabled = Boolean(config.data?.masterEnabled && game?.enabled);
  const canStart = gamesAuthorityKnown && fishingStateKnown && featureEnabled && uncertainWriteCount === 0;
  useEffect(() => {
    gamesAuthorityKnownRef.current = gamesAuthorityKnown;
    fishingStateKnownRef.current = fishingStateKnown;
  }, [fishingStateKnown, gamesAuthorityKnown]);
  const l4Active = (session.data?.user.effective_level ?? 0) >= 4;
  const [reducedMotion, setReducedMotion] = useState(prefersReducedMotion);
  const previousReducedMotion = useRef(reducedMotion);
  const refetchConfig = config.refetch;

  // Credits are owned by /api/games. Mutation responses, prices, animations,
  // and result payloads never write the balance cache directly. Every
  // uncertain or recovery path calls this helper so a stale balance is
  // replaced only by a fresh authoritative response.
  const refetchGamesAuthority = useCallback(async (): Promise<boolean> => {
    const token = authorityRefreshToken.current + 1;
    authorityRefreshToken.current = token;
    setGamesAuthorityUnknown(true);
    try {
      const refreshed = await refetchConfig();
      const success = refreshed.isSuccess && !refreshed.isError && Boolean(refreshed.data);
      if (authorityRefreshToken.current === token) {
        setGamesAuthorityUnknown(!success);
      }
      return success;
    } catch {
      // The existing query error state remains visible; retrying the feature
      // must not turn a failed authority read into a derived balance.
      if (authorityRefreshToken.current === token) {
        setGamesAuthorityUnknown(true);
      }
      return false;
    }
  }, [refetchConfig]);

  useEffect(() => {
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return undefined;
    const media = window.matchMedia(REDUCED_MOTION_QUERY);
    const onChange = (event: MediaQueryListEvent) => setReducedMotion(event.matches);
    if (typeof media.addEventListener === 'function') {
      media.addEventListener('change', onChange);
      return () => media.removeEventListener('change', onChange);
    }
    media.addListener(onChange);
    return () => media.removeListener(onChange);
  }, []);

  const deferResult = useCallback((result: FishingResult) => {
    if (acknowledgedRounds.current.has(result.roundId)) return;
    const appended = appendDeferredResult(deferredResults.current, result);
    if (appended.overflowed) {
      // Keep all locally retained results intact.  Any result that does not
      // fit is recovered from the authoritative state endpoint after ACKs.
      void refetchFishingStateAuthority();
      void refetchGamesAuthority();
      return;
    }
    deferredResults.current = [...appended.results];
    setDeferredResultList(appended.results);
  }, [refetchFishingStateAuthority, refetchGamesAuthority]);

  const completeAcknowledgedResult = useCallback((roundId: string, nextResult: FishingResult | null, restoreFocus: boolean) => {
    rememberAcknowledgedRound(roundId);
    deferredResults.current = deferredResults.current.filter((candidate) => candidate.roundId !== roundId);
    setDeferredResultList((current) => current.filter((candidate) => candidate.roundId !== roundId));
    setSettledResultIds((current) => {
      if (!current.has(roundId)) return current;
      const next = new Set(current);
      next.delete(roundId);
      return next;
    });
    if (restoreFocus) ackFocusRequested.current = true;

    // Replace the local presentation with the next server-owned result when
    // an ACK response was lost after the server had already advanced the
    // unrevealed queue.  A newer busy round remains authoritative and must not
    // be reset by an older ACK callback.
    const currentFlow = flowRef.current;
    if (currentFlow.result?.roundId !== roundId || isFishingBusy(currentFlow.phase)) return;
    presentationGeneration.current += 1;
    if (nextResult && nextResult.roundId !== roundId) {
      dispatch({ type: 'recovered', result: nextResult });
    } else {
      dispatch({ type: 'acknowledged' });
    }
  }, [rememberAcknowledgedRound]);

  const settleRound = useCallback(async (pending: PendingRound, explicitRetry = false) => {
    if (settlingRound.current === pending.roundId) return;
    // Animation timers are presentation-only.  Never turn a timer firing
    // during an uncertain config/state read into an economic POST.  The
    // recovery effect below retries only after both authorities are healthy
    // and still report this exact pending round.
    const authoritativeState = queryClient.getQueryData<FishingState>(fishingKeys.state);
    const hasMatchingPending = authoritativeState?.pendingRound?.roundId === pending.roundId;
    const canRetryTerminalCorrelation = explicitRetry
      && invalidSettlementContext?.roundId === pending.roundId;
    if (
      !gamesAuthorityKnownRef.current
      || !fishingStateKnownRef.current
      || !authoritativeState
      || (!hasMatchingPending && !canRetryTerminalCorrelation)
    ) return;
    const generation = settleGeneration.current + 1;
    settleGeneration.current = generation;
    const settlementWriteToken = markWriteUncertain();
    activeSettlement.current = { generation, pending, writeToken: settlementWriteToken };
    settlingRound.current = pending.roundId;
    setInvalidSettlementRound(null);
    setInvalidSettlementContext(null);
    dispatch({ type: 'phase', phase: 'settling' });
    try {
      const result = await settle.mutateAsync(pending.roundId);
      // A late response must never move a newer flow back to an older result.
      if (activeSettlement.current?.generation !== generation) {
        void refetchFishingStateAuthority(settlementWriteToken);
        void refetchGamesAuthority();
        return;
      }
      if (result.roundId !== pending.roundId || result.bait !== pending.bait || result.price !== pending.price) {
        throw new ApiError('invalid_response', 'The server returned a settlement for a different pending round.', 200);
      }
      // Only after the complete correlation and generation checks may the
      // balance authority be reread. The result payload itself never updates
      // the cached credits.
      void refetchFishingStateAuthority(settlementWriteToken);
      void refetchGamesAuthority();
      activePendingRound.current = null;
      clearSettlementRetry();
      setSettledResultIds((current) => {
        const next = new Set(current);
        next.add(result.roundId);
        while (next.size > MAX_ACKNOWLEDGED_ROUNDS) {
          const oldest = next.values().next().value;
          if (oldest === undefined) break;
          next.delete(oldest);
        }
        return next;
      });
      const olderResult = queryClient.getQueryData<FishingState>(fishingKeys.state)?.unrevealedResult;
      const hasOlderResult = Boolean(
        (olderResult && olderResult.roundId !== result.roundId && !acknowledgedRounds.current.has(olderResult.roundId))
        || deferredResults.current.some((candidate) => candidate.roundId !== result.roundId),
      );
      if (hasOlderResult) {
        deferResult(result);
      }
      dispatch({ type: 'settled', result });
    } catch (error) {
      if (activeSettlement.current?.generation !== generation) {
        void refetchFishingStateAuthority(settlementWriteToken);
        void refetchGamesAuthority();
        return;
      }
      // The follow-up state read decides whether this is still a payable
      // pending round.  If it is already committed/released, the retry marker
      // is cleared and a terminal unrevealed result (if any) is recoverable.
      retryPendingRound.current = pending;
      setRetryPendingRoundState(pending);
      setInvalidSettlementRound(pending.roundId);
      setInvalidSettlementContext(pending);
      dispatch({ type: 'failed', error: error instanceof Error ? error : new Error('Settlement failed.') });
      void refetchFishingStateAuthority(settlementWriteToken);
      void refetchGamesAuthority();
    } finally {
      if (activeSettlement.current?.generation === generation) activeSettlement.current = null;
      if (settlingRound.current === pending.roundId) settlingRound.current = null;
    }
  }, [clearSettlementRetry, deferResult, invalidSettlementContext, markWriteUncertain, queryClient, refetchFishingStateAuthority, refetchGamesAuthority, settle]);

  const clearTimers = useCallback(() => {
    for (const timer of timers.current) window.clearTimeout(timer);
    timers.current = [];
  }, []);

  useEffect(() => clearTimers, [clearTimers]);

  const animateRound = useCallback((pending: PendingRound, restart = false, expectedPresentationGeneration?: number) => {
    if (expectedPresentationGeneration !== undefined && presentationGeneration.current !== expectedPresentationGeneration) {
      const authoritativePendingRoundId = queryClient.getQueryData<FishingState>(fishingKeys.state)?.pendingRound?.roundId;
      if (authoritativePendingRoundId !== pending.roundId && activePendingRound.current?.roundId !== pending.roundId) return false;
    }
    if (expectedPresentationGeneration !== undefined
      && activePendingRound.current
      && activePendingRound.current.roundId !== pending.roundId) return false;
    const previousRoundId = activePendingRound.current?.roundId
      ?? retryPendingRound.current?.roundId
      ?? activeSettlement.current?.pending.roundId
      ?? flowRef.current.roundId
      ?? invalidSettlementRound;
    const supersedesPreviousRound = previousRoundId !== null && previousRoundId !== pending.roundId;
    if (supersedesPreviousRound) {
      // A new authoritative pending round supersedes every presentation of
      // an older round, including a settle request that already failed and
      // cleared activeSettlement. Cancel its timers/generation and remove
      // retry/error markers before driving the new server-owned round.
      settleGeneration.current += 1;
      activeSettlement.current = null;
      settlingRound.current = null;
      retryPendingRound.current = null;
      setRetryPendingRoundState(null);
      setInvalidSettlementRound(null);
      setInvalidSettlementContext(null);
    }
    if (!restart && animatedRound.current === pending.roundId && !supersedesPreviousRound) return true;
    if (activePendingRound.current?.roundId !== pending.roundId) {
      if (expectedPresentationGeneration === undefined) presentationGeneration.current += 1;
    }
    animatedRound.current = pending.roundId;
    activePendingRound.current = pending;
    clearTimers();
    dispatch({ type: 'begin', roundId: pending.roundId });
    const multiplier = reducedMotion ? 0 : 1;
    const phases: Array<{ phase: 'waiting' | 'reeling' | 'settling'; delay: number }> = [
      { phase: 'waiting', delay: FISHING_PHASE_DURATIONS_MS.casting * multiplier },
      { phase: 'reeling', delay: (FISHING_PHASE_DURATIONS_MS.casting + FISHING_PHASE_DURATIONS_MS.waiting) * multiplier },
      { phase: 'settling', delay: (FISHING_PHASE_DURATIONS_MS.casting + FISHING_PHASE_DURATIONS_MS.waiting + FISHING_PHASE_DURATIONS_MS.reeling) * multiplier },
    ];
    for (const entry of phases) {
      timers.current.push(window.setTimeout(() => {
        if (entry.phase === 'settling') {
          // Keep the presentation in a visible settling state even when the
          // authority gate defers the POST until a later successful read.
          dispatch({ type: 'phase', phase: 'settling' });
          void settleRound(pending);
        } else {
          dispatch({ type: 'phase', phase: entry.phase });
        }
      }, entry.delay));
    }
    return true;
  }, [clearTimers, invalidSettlementRound, queryClient, reducedMotion, settleRound]);

  useEffect(() => {
    if (previousReducedMotion.current === reducedMotion) return;
    previousReducedMotion.current = reducedMotion;
    if (flow.phase !== 'casting' && flow.phase !== 'waiting' && flow.phase !== 'reeling') return;
    const pending = state.data?.pendingRound ?? activePendingRound.current;
    if (pending) animateRound(pending, true);
  }, [animateRound, flow.phase, reducedMotion, state.data?.pendingRound]);

  useEffect(() => {
    if (flow.phase !== 'settling' || settlingRound.current !== null) return;
    const pending = activePendingRound.current;
    const authoritativeState = queryClient.getQueryData<FishingState>(fishingKeys.state);
    if (!pending || !gamesAuthorityKnown || !fishingStateKnown || authoritativeState?.pendingRound?.roundId !== pending.roundId) return;
    void settleRound(pending);
  }, [flow.phase, fishingStateKnown, gamesAuthorityKnown, queryClient, settleRound, state.data?.pendingRound?.roundId]);

  useEffect(() => {
    const pending = state.data?.pendingRound;
    const recoveredStart = pending && flow.phase === 'error' && !flow.roundId && !flow.result;
    const supersedingPending = Boolean(
      pending
      && (
        (activeSettlement.current && activeSettlement.current.pending.roundId !== pending.roundId)
        || (activePendingRound.current && activePendingRound.current.roundId !== pending.roundId)
        || (retryPendingRound.current && retryPendingRound.current.roundId !== pending.roundId)
        || (flow.roundId !== null && flow.roundId !== pending.roundId)
        || (invalidSettlementRound !== null && invalidSettlementRound !== pending.roundId)
      ),
    );
    if (pending && (flow.phase === 'idle' || recoveredStart || supersedingPending)) {
      if (recoveredStart) start.reset();
      if (supersedingPending && flow.result && !acknowledgedRounds.current.has(flow.result.roundId)) {
        deferResult(flow.result);
      }
      animateRound(pending);
    }
  }, [animateRound, deferResult, flow.phase, flow.result, flow.roundId, invalidSettlementRound, start, state.data?.pendingRound]);

  useEffect(() => {
    const authoritative = state.data;
    if (!fishingStateKnown || !authoritative || authoritative.pendingRound) return;
    // A failed settle can race the worker or an idempotent replay.  Once the
    // authoritative projection is terminal, its old retry marker must never
    // keep blocking a new round.  Any unrevealed result is handled by the
    // recovery effect below; a released/no-result round simply clears these
    // presentation-only markers.
    const failedPending = retryPendingRound.current;
    if (failedPending) {
      const resultMatchesFailedRound = authoritative.unrevealedResult
        && authoritative.unrevealedResult.roundId === failedPending.roundId
        && authoritative.unrevealedResult.bait === failedPending.bait
        && authoritative.unrevealedResult.price === failedPending.price;
      // Keep only the correlation guard when a malformed settle response is
      // followed by an unrelated terminal result; it must not be rendered as
      // the failed round.  A matching/absent terminal projection is safe to
      // clear and no longer needs a retry marker.
      if (!authoritative.unrevealedResult || resultMatchesFailedRound) {
        retryPendingRound.current = null;
        setRetryPendingRoundState(null);
        setInvalidSettlementRound(null);
        setInvalidSettlementContext(null);
      } else {
        setInvalidSettlementContext(failedPending);
      }
    }
    if (flow.phase === 'settling') {
      clearTimers();
      settleGeneration.current += 1;
      activeSettlement.current = null;
      settlingRound.current = null;
      activePendingRound.current = null;
      // Leave terminal projection handling to the recovery effect below. A
      // result with a different round id may be a queued outcome from another
      // device; reset the presentation first so it can be shown as an
      // independent recovered result instead of leaving the old settling flow
      // permanently busy behind the correlation guard.
      dispatch({ type: 'reset' });
    }
  }, [clearSettlementRetry, clearTimers, flow.phase, fishingStateKnown, state.data]);

  useEffect(() => {
    const result = state.data?.unrevealedResult;
    if (!fishingStateKnown || !result || acknowledgedRounds.current.has(result.roundId)) return;
    // A worker may finish a round after a manual settle error while this page
    // remains open. Once the authoritative state has no pending round, the
    // same recovery path is valid from either idle or the retryable error UI.
    if (flow.phase !== 'idle' && flow.phase !== 'error' && flow.phase !== 'settling') return;
    if (state.data?.pendingRound) return;
    // A malformed settle response must not be replaced by an unrelated result
    // from the follow-up state query.  The caller can retry the same round.
    if (flow.phase === 'error' && flow.roundId && result.roundId !== flow.roundId) return;
    const pendingContext = retryPendingRound.current ?? invalidSettlementContext;
    if (
      flow.phase === 'error'
      && flow.roundId
      && result.roundId === flow.roundId
      && pendingContext
      && (result.bait !== pendingContext.bait || result.price !== pendingContext.price)
    ) return;
    if (flow.result?.roundId === result.roundId) return;
    // A start intentionally defers the currently displayed unACKed result.
    // Do not replay that same state row into the idle reducer while the start
    // POST is in flight; doing so would consume the new start generation and
    // make its otherwise-current response look stale.
    if (activeStartAttempt.current?.baselineResultId === result.roundId) return;
    // A worker or another device may have completed the round. Refresh the
    // balance from the authority endpoint before presenting recovery state;
    // never infer it from the result or animation.
    queueMicrotask(() => { void refetchGamesAuthority(); });
    presentationGeneration.current += 1;
    dispatch({ type: 'recovered', result });
  }, [fishingStateKnown, flow.phase, flow.roundId, flow.result?.roundId, invalidSettlementContext, invalidSettlementRound, refetchGamesAuthority, state.data?.pendingRound, state.data?.unrevealedResult]);

  const serverResultId = state.data?.unrevealedResult?.roundId;
  const flowResultId = flow.result?.roundId;

  useEffect(() => {
    const liveIds = new Set<string>();
    if (serverResultId) liveIds.add(serverResultId);
    if (flowResultId) liveIds.add(flowResultId);
    for (const result of deferredResultList) liveIds.add(result.roundId);
    let changed = false;
    for (const roundId of acknowledgedRounds.current) {
      if (!liveIds.has(roundId)) {
        acknowledgedRounds.current.delete(roundId);
        changed = true;
      }
    }
    if (changed) setAcknowledgedRoundIds(new Set(acknowledgedRounds.current));
  }, [deferredResultList, flowResultId, serverResultId]);

  const isCurrentStartAttempt = useCallback((attempt: StartAttempt, response?: PendingRound): boolean => {
    if (activeStartAttempt.current?.token !== attempt.token) {
      return false;
    }

    // Read the query cache at the decision point rather than the render that
    // launched the request. A state refetch can commit while the POST is in
    // flight, before this async callback resumes.
    const currentState = queryClient.getQueryData<FishingState>(fishingKeys.state);
    const currentPendingRoundId = currentState?.pendingRound?.roundId ?? null;
    const currentResultId = currentState?.unrevealedResult?.roundId ?? null;
    const currentAnimatedRoundId = activePendingRound.current?.roundId ?? null;
    // React Query can invalidate Fishing state from the mutation's onSuccess
    // callback before mutateAsync resumes. If that read already animated the
    // exact response round, its generation advance is part of this same
    // authoritative presentation; every other generation change is stale.
    if (presentationGeneration.current !== attempt.presentationGeneration
      && (!response || currentPendingRoundId !== response.roundId || currentAnimatedRoundId !== response.roundId)) {
      return false;
    }
    if (response) {
      if (currentPendingRoundId && currentPendingRoundId !== response.roundId) return false;
      if (currentAnimatedRoundId && currentAnimatedRoundId !== response.roundId) return false;
    } else {
      if (currentPendingRoundId !== null && currentPendingRoundId !== attempt.baselinePendingRoundId) return false;
      if (currentAnimatedRoundId !== null && currentAnimatedRoundId !== attempt.baselinePendingRoundId) return false;
    }
    if (currentResultId !== null && currentResultId !== attempt.baselineResultId) return false;
    return true;
  }, [queryClient]);

  const startRound = async () => {
    if (!selected || !canStart || hasPending || start.isPending || ack.isPending || acknowledgingRound.current !== null) return;
    const currentState = queryClient.getQueryData<FishingState>(fishingKeys.state) ?? state.data;
    const attempt: StartAttempt = {
      token: startAttemptToken.current + 1,
      presentationGeneration: presentationGeneration.current + 1,
      bait: selected.id,
      baselinePendingRoundId: currentState?.pendingRound?.roundId ?? null,
      baselineResultId: currentState?.unrevealedResult?.roundId ?? flow.result?.roundId ?? null,
      writeToken: markWriteUncertain(),
    };
    startAttemptToken.current = attempt.token;
    activeStartAttempt.current = attempt;
    presentationGeneration.current = attempt.presentationGeneration;
    if (flow.result && !acknowledgedRounds.current.has(flow.result.roundId)) deferResult(flow.result);
    dispatch({ type: 'reset' });
    try {
      const response = await start.mutateAsync({
        bait: attempt.bait,
        idempotencyKey: createIdempotencyKey(),
      });
      if (!isCurrentStartAttempt(attempt, response)) {
        // The response belongs to a presentation that has already been
        // superseded by authoritative state or another device. It must not
        // animate or surface an error; only reconcile both authorities.
        void refetchFishingStateAuthority(attempt.writeToken);
        void refetchGamesAuthority();
        return;
      }
      // The start wire contains a server snapshot, but /api/games remains the
      // sole balance authority. Re-read it before driving either presentation
      // branch so a stale or replayed response cannot supply the cached balance.
      void refetchGamesAuthority();
      if (response.state === 'pending') {
        animateRound(response, false, attempt.presentationGeneration);
      } else {
        // An idempotent replay may report a committed/released terminal
        // state without creating a new pending round.  Do not carry an old
        // settlement retry marker into that terminal outcome.
        clearSettlementRetry();
        void refetchFishingStateAuthority(attempt.writeToken);
      }
      if (response.state === 'pending') void refetchFishingStateAuthority(attempt.writeToken);
    } catch (error) {
      if (!isCurrentStartAttempt(attempt)) {
        // A delayed error is just as stale as a delayed success. Preserve the
        // newer authoritative presentation and reconcile instead of dispatching
        // a failure into it.
        start.reset();
        void refetchFishingStateAuthority(attempt.writeToken);
        void refetchGamesAuthority();
        return;
      }
      dispatch({ type: 'failed', error: error instanceof Error ? error : new Error('Start failed.') });
      void refetchFishingStateAuthority(attempt.writeToken);
      void refetchGamesAuthority();
    } finally {
      if (activeStartAttempt.current?.token === attempt.token) activeStartAttempt.current = null;
    }
  };

  const acknowledge = async (result: FishingResult, ackButton?: HTMLButtonElement) => {
    if (
      ack.isPending
      || acknowledgingRound.current !== null
      || acknowledgedRounds.current.has(result.roundId)
      || !fishingStateKnown
      || state.isPending
      || state.isFetching
      || state.isError
      || fishingStateAuthorityUnknown
      || uncertainWriteCount > 0
    ) return;
    const generation = presentationGeneration.current;
    const restoreFocus = Boolean(ackButton && document.activeElement === ackButton);
    const acknowledgementWriteToken = markWriteUncertain();
    acknowledgingRound.current = result.roundId;
    if (flow.result && flow.result.roundId !== result.roundId) deferResult(flow.result);
    try {
      await ack.mutateAsync(result.roundId);
      const authoritative = queryClient.getQueryData<FishingState>(fishingKeys.state);
      completeAcknowledgedResult(result.roundId, authoritative?.unrevealedResult ?? null, restoreFocus);
      if (retryPendingRound.current?.roundId === result.roundId) {
        clearSettlementRetry();
      }
      // An older result may be acknowledged while a newer pending round is
      // animating. Do not reset that active presentation or lose its result.
      const currentFlow = flowRef.current;
      if (
        presentationGeneration.current === generation
        && currentFlow.result?.roundId === result.roundId
        && !isFishingBusy(currentFlow.phase)
      ) {
        dispatch({ type: 'acknowledged' });
      }
      void refetchFishingStateAuthority(acknowledgementWriteToken);
    } catch {
      // An ACK response can be lost after the server commits it. Keep the
      // result visible while the authoritative state read decides whether to
      // remove it. If the server has already advanced to another result,
      // replace the local card with that server-owned result instead.
      void refetchFishingStateAuthority(acknowledgementWriteToken).then((success) => {
        if (!success) return;
        const authoritative = queryClient.getQueryData<FishingState>(fishingKeys.state);
        if (authoritative?.unrevealedResult?.roundId === result.roundId) return;
        completeAcknowledgedResult(result.roundId, authoritative?.unrevealedResult ?? null, restoreFocus);
      });
    }
    finally {
      if (acknowledgingRound.current === result.roundId) acknowledgingRound.current = null;
    }
  };

  const retrySettlement = () => {
    if (!gamesAuthorityKnown || !fishingStateKnown || uncertainWriteCount > 0) return;
    const pending = retryPendingRound.current;
    if (pending) void settleRound(pending, true);
  };

  const currentResult = flow.result;
  const serverResult = state.data?.unrevealedResult ?? null;
  const safeServerResult = invalidSettlementRound
    && flow.phase === 'error'
    && flow.roundId === invalidSettlementRound
    && serverResult
    && (
      serverResult.roundId !== invalidSettlementRound
      || (
        (retryPendingRoundState ?? invalidSettlementContext)
        && (
          serverResult.bait !== (retryPendingRoundState ?? invalidSettlementContext)?.bait
          || serverResult.price !== (retryPendingRoundState ?? invalidSettlementContext)?.price
        )
      )
    )
    ? null
    : serverResult;
  const safeCurrentResult = invalidSettlementRound
    && flow.phase === 'error'
    && flow.roundId === invalidSettlementRound
    && currentResult
    && (
      currentResult.roundId !== invalidSettlementRound
      || (
        (retryPendingRoundState ?? invalidSettlementContext)
        && (
          currentResult.bait !== (retryPendingRoundState ?? invalidSettlementContext)?.bait
          || currentResult.price !== (retryPendingRoundState ?? invalidSettlementContext)?.price
        )
      )
    )
    ? null
    : currentResult;
  const resultQueue: FishingResult[] = [];
  for (const candidate of [safeServerResult, ...deferredResultList, safeCurrentResult]) {
    if (candidate && !acknowledgedRoundIds.has(candidate.roundId) && !resultQueue.some((item) => item.roundId === candidate.roundId)) {
      resultQueue.push(candidate);
    }
  }
  const displayedResult = resultQueue[0] ?? null;
  const resultHasMore = Boolean(state.data?.hasMoreUnrevealed || resultQueue.length > 1);
  useEffect(() => {
    if (!ackFocusRequested.current) return;
    ackFocusRequested.current = false;
    // The ACK button is intentionally removed after authoritative cleanup.
    // Prefer the next result's action, otherwise return to the stable cast
    // control; if it is temporarily disabled during reconciliation, the
    // stage heading remains a keyboard-visible stable landmark.
    let cancelled = false;
    const timer = window.setTimeout(() => {
      if (cancelled) return;
      const nextAck = document.querySelector<HTMLButtonElement>('[data-testid="fishing-result-ack"]');
      if (nextAck && !nextAck.disabled) {
        nextAck.focus();
        return;
      }
      const cast = castButtonRef.current;
      if (cast && !cast.disabled) {
        cast.focus();
        return;
      }
      const stageTitle = document.getElementById('fishing-stage-title');
      stageTitle?.focus();
    }, 0);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [displayedResult?.roundId]);
  const canAfford = gamesAuthorityKnown && selected ? isAffordable(config.data?.credits ?? '', selected.price) : false;
  const serverProfilePublic = config.data?.gameProfilePublic ?? false;
  const profileAuthorityPending = profileReconciling
    || config.isPending || config.isFetching
    || single.isPending || single.isFetching
    || total.isPending || total.isFetching;
  const profileAuthorityError = Boolean(config.error || single.error || total.error);
  const profileAuthorityUncertain = profileAuthorityPending
    || profileAuthorityError
    || !config.data || !single.data || !total.data;
  // While any of the three authority reads is in flight, fail closed. Once
  // they all succeed, the current config response is the only source of the
  // public/private decision; no local override survives a later refetch.
  const profilePublic = profileAuthorityUncertain ? false : serverProfilePublic;
  const profileCheckboxValue = profileAuthorityUncertain ? false : profilePublic;
  const projectedSingle = projectLeaderboardPrivacy(single.data, profilePublic);
  const projectedTotal = projectLeaderboardPrivacy(total.data, profilePublic);

  const reconcileProfile = useCallback(async () => {
    setProfileReconciling(true);
    const results = await Promise.allSettled([config.refetch(), single.refetch(), total.refetch()]);
    const configResult = results[0];
    const allRefetched = results.every((result) => result.status === 'fulfilled' && result.value.isSuccess);
    const authoritative = configResult.status === 'fulfilled' ? configResult.value.data : undefined;
    if (allRefetched && authoritative && typeof authoritative.gameProfilePublic === 'boolean') {
      setProfileReconciling(false);
      return;
    }
    // Keep the page masked and visibly unknown if any authority read failed.
    // The retry action below repeats all three reads; a local override never
    // turns a partial/stale response into a confirmed public projection.
    setProfileReconciling(true);
  }, [config, single, total]);

  const updateProfileVisibility = (next: boolean) => {
    // Every transition starts masked and unknown.  The requested value is
    // never rendered as an authoritative privacy result while PATCH or the
    // three follow-up reads are in flight.
    setProfileReconciling(true);
    profile.mutate(next, {
      onSuccess: () => { void reconcileProfile(); },
      onError: () => { void reconcileProfile(); },
    });
  };

  if (config.isPending) return <LoadingState label={gameText(t, 'games.fishing.loading', 'Loading fishing…')} />;
  // A failed revalidation may leave a usable previous config snapshot. Keep
  // the page mounted in that case so privacy/economy recovery controls remain
  // available; only the initial absence of config is a blocking load error.
  if (!config.data) return <ErrorState error={config.error ?? new ApiError('invalid_response', 'Missing game configuration.', 200)} onRetry={() => void config.refetch()} />;

  return (
    <div className="page fishing-page">
      <PageHeader
        eyebrow={gameText(t, 'games.fishing.eyebrow', 'Games · Fishing')}
        title={gameText(t, 'games.fishing.title', 'Pond fishing')}
        description={gameText(t, 'games.fishing.description', 'Choose bait, cast a line, and let the server reveal your catch.')}
      />

      <Card className="fishing-balance">
        <div className="fishing-balance__row">
          <strong>{gameText(t, 'games.fishing.balance', 'Available credits')}</strong>
          <span className="fishing-balance__amount" role={!gamesAuthorityKnown ? 'status' : undefined}>
            {gamesAuthorityKnown
              ? <CreditAmount value={config.data.credits} />
              : gameText(t, 'games.fishing.balanceUnknown', 'Unknown until the server confirms the balance')}
          </span>
        </div>
        <span className="fishing-card__hint">
          {gamesAuthorityKnown
            ? gameText(t, 'games.fishing.balanceHint', 'All game costs and payouts are decided by the server.')
            : gameText(t, 'games.fishing.balanceUnknownHint', 'Casting is disabled until the server confirms the current balance.')}
        </span>
        {!gamesAuthorityKnown ? (
          <div className="fishing-balance__recovery">
            <p className="fishing-alert" role="alert">
              {gameText(t, 'games.fishing.balanceUnknownAlert', 'The current balance could not be confirmed. No new round can start.')}
            </p>
            <button
              type="button"
              className="btn btn-secondary"
              onClick={() => void refetchGamesAuthority()}
              disabled={config.isFetching}
            >
              {config.isFetching
                ? gameText(t, 'games.fishing.balanceRetrying', 'Checking balance…')
                : gameText(t, 'games.fishing.balanceRetry', 'Retry balance check')}
            </button>
          </div>
        ) : null}
      </Card>

      {gamesAuthorityKnown && !featureEnabled ? (
        <Card className="fishing-disabled">
          <div role="status">
            <h2>{gameText(t, 'games.fishing.disabledTitle', 'Fishing is not open')}</h2>
            <p className="fishing-card__hint">{gameText(t, 'games.fishing.disabledBody', 'The administrator has not enabled this game yet. Your balance is unchanged.')}</p>
          </div>
        </Card>
      ) : null}

      <div className="fishing-layout">
        <div className="fishing-page__main">
          <FishingStage
            flow={flow}
            pending={state.data?.pendingRound ?? null}
            onRetry={retrySettlement}
            retryDisabled={settle.isPending || !gamesAuthorityKnown || !fishingStateKnown || state.isPending || state.isFetching || state.isError || uncertainWriteCount > 0}
            canRetry={flow.phase === 'error' && Boolean(retryPendingRoundState)}
            l4Active={l4Active}
          />
          <Card className="fishing-card fishing-bait-card">
            <div className="fishing-card__heading">
              <div>
                <h2>{gameText(t, 'games.fishing.baitsTitle', 'Choose bait')}</h2>
                <p className="fishing-card__hint">{gameText(t, 'games.fishing.baitsHint', 'The server validates the price and balance before each cast.')}</p>
              </div>
              <span className="fishing-card__hint">{gameText(t, 'games.fishing.ticket', 'One ticket')}</span>
            </div>
            <div className="fishing-bait-grid" role="list" aria-label={gameText(t, 'games.fishing.baitsLabel', 'Fishing bait')}>
              {baits.map((bait: FishingBait) => {
                const affordable = gamesAuthorityKnown && isAffordable(config.data.credits, bait.price);
                const unavailable = !canStart || hasPending || !affordable;
                const reason = !gamesAuthorityKnown
                  ? gameText(t, 'games.fishing.balanceUnknown', 'Balance unavailable until the server confirms it')
                  : !featureEnabled
                    ? gameText(t, 'games.fishing.errors.disabled', 'Fishing is currently closed')
                  : hasPending
                    ? gameText(t, 'games.fishing.errors.pending', 'Finish the pending round first')
                    : !affordable
                      ? gameText(t, 'games.fishing.errors.insufficient', 'Not enough available credits')
                      : '';
                return (
                  <div key={bait.id} role="listitem" className="fishing-bait-item">
                    <button
                      type="button"
                      className={`fishing-bait${selectedBait === bait.id ? ' is-selected' : ''}`}
                      aria-pressed={selectedBait === bait.id}
                      aria-label={`${baitName(t, bait.id)} · ${amountLabel(bait.price).display} ${gameText(t, 'games.fishing.creditsUnit', 'credits')}${reason ? ` · ${reason}` : ''}`}
                      onClick={() => setSelectedBait(bait.id)}
                      disabled={unavailable}
                    >
                      <span className="fishing-bait__name">{baitName(t, bait.id)}</span>
                      <span className="fishing-bait__price"><CreditAmount value={bait.price} /></span>
                      {reason ? <span className="fishing-bait__reason">{reason}</span> : null}
                    </button>
                  </div>
                );
              })}
            </div>
            <div className="fishing-game-actions">
              <span className="fishing-status-line" role="status">
                {start.error ? errorReason(t, start.error) : flow.error ? errorReason(t, flow.error) : null}
              </span>
              <button
                type="button"
                className="btn btn-primary"
                data-testid="fishing-cast"
                ref={castButtonRef}
                onClick={() => void startRound()}
                disabled={!selected || !canStart || hasPending || !canAfford || start.isPending || settle.isPending || ack.isPending || uncertainWriteCount > 0}
              >
                {start.isPending ? gameText(t, 'games.fishing.castWorking', 'Casting…') : gameText(t, 'games.fishing.cast', 'Cast line')}
              </button>
            </div>
            {state.error ? <ErrorState error={state.error} onRetry={() => { void refetchFishingStateAuthority(); void refetchGamesAuthority(); }} /> : null}
          </Card>

          {displayedResult ? (
            <ResultCard
              result={displayedResult}
              source={settledResultIds.has(displayedResult.roundId) ? 'settled' : 'recovered'}
              hasMore={resultHasMore}
              onAck={(button) => void acknowledge(displayedResult, button)}
              ackPending={ack.isPending}
              ackDisabled={!fishingStateKnown
                || state.isPending
                || state.isFetching
                || state.isError
                || fishingStateAuthorityUnknown
                || uncertainWriteCount > 0}
              balanceKnown={gamesAuthorityKnown}
              balance={config.data.credits}
            />
          ) : null}
          {ack.error ? <p className="fishing-alert" role="alert">{errorReason(t, ack.error)}</p> : null}
        </div>

        <aside className="fishing-page__aside">
          <Card className="fishing-profile">
            <div className="fishing-profile__row">
              <div>
                <h2>{gameText(t, 'games.fishing.profile.title', 'Leaderboard profile')}</h2>
                <p className="fishing-profile__hint">{gameText(t, 'games.fishing.profile.hint', 'Anonymous by default. This preference applies to all games.')}</p>
              </div>
              <label className="fishing-profile__toggle">
                <input
                  type="checkbox"
                  checked={profileCheckboxValue}
                  disabled={profile.isPending || profileAuthorityUncertain}
                  onChange={(event) => updateProfileVisibility(event.target.checked)}
                />
                <span>{gameText(t, 'games.fishing.profile.public', 'Show my profile')}</span>
              </label>
            </div>
            <p className="fishing-profile__hint" role={profileAuthorityUncertain ? 'status' : undefined}>
              {profileAuthorityUncertain
                ? gameText(t, 'games.fishing.profile.reconciling', 'Privacy status is being rechecked with the server…')
                : profilePublic
                ? gameText(t, 'games.fishing.profile.publicWarning', 'Your current username, avatar, and L4 badge may appear together on public rows.')
                : gameText(t, 'games.fishing.profile.privateWarning', 'Your username, avatar, and L4 badge stay hidden from leaderboard rows.')}
            </p>
            {profileAuthorityError ? (
              <p className="fishing-alert" role="alert">
                {gameText(t, 'games.fishing.profile.uncertain', 'Privacy status could not be confirmed. Your identity is hidden until you retry.')}
              </p>
            ) : null}
            {profileAuthorityUncertain ? (
              <div className="fishing-profile__actions">
                <button
                  type="button"
                  className="btn btn-secondary fishing-profile__retry"
                  onClick={() => void reconcileProfile()}
                  disabled={profile.isPending || config.isFetching || single.isFetching || total.isFetching}
                >
                  {gameText(t, 'games.fishing.profile.retry', 'Retry privacy check')}
                </button>
                <button
                  type="button"
                  className="btn btn-secondary fishing-profile__private-now"
                  onClick={() => updateProfileVisibility(false)}
                  disabled={profile.isPending}
                >
                  {gameText(t, 'games.fishing.profile.makePrivate', 'Hide my profile now')}
                </button>
              </div>
            ) : null}
            {profile.error ? <p className="fishing-alert" role="alert">{errorReason(t, profile.error)}</p> : null}
          </Card>
          <div className="fishing-leaderboards">
            <LeaderboardCard board="single" data={projectedSingle} error={single.error} isPending={single.isPending} onRetry={() => void single.refetch()} />
            <LeaderboardCard board="total" data={projectedTotal} error={total.error} isPending={total.isPending} onRetry={() => void total.refetch()} />
          </div>
        </aside>
      </div>
    </div>
  );
}
