import { useCallback, useEffect, useRef, useState } from 'react';
import { Link } from 'react-router';
import { useQueryClient } from '@tanstack/react-query';
import { Card, ErrorState, LoadingState, PageHeader } from '@shared/components/States';
import { useUserSession } from '../../data';
import { useGameCopy } from '../copy';
import { GameRulesButton, GameRulesDialog, type GameRulesSection } from '../common/GameRulesDialog';
import {
  createIdempotencyKey,
  isConflict,
  isMaintenance,
  isResponseUnknown,
  isServiceStopped,
} from '../common/request';
import { creditsToMilli, formatCredits, multiplyCredits } from '../common/strict';
import { BAITS, type Bait } from '../common/types';
import { gameKeys, useGamesSnapshot } from '../common/snapshot';
import { FishingArtwork } from './FishingArtwork';
import {
  acknowledgeFishing,
  fishingKeys,
  isFishingResult,
  recoverFishing,
  startFishing,
  useFishingLeaderboard,
  useFishingState,
} from './api';
import { FISHING_REVEAL_MS, fishingPresentationPhase, nextRevealCount } from './stateMachine';
import type {
  FishingBatchResult,
  FishingLeaderboard,
  FishingLeaderboard as Leaderboard,
  FishingSingleRow,
  FishingStartIntent,
  FishingTotalRow,
} from './types';
import { fishingItemName } from './text';
import './fishing.css';

function useReducedMotion(): boolean {
  const [reduced, setReduced] = useState(false);
  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return undefined;
    const media = window.matchMedia('(prefers-reduced-motion: reduce)');
    const update = () => setReduced(media.matches);
    update();
    media.addEventListener?.('change', update);
    return () => media.removeEventListener?.('change', update);
  }, []);
  return reduced;
}

function Money({ value }: { readonly value: string }) {
  const { text } = useGameCopy();
  return (
    <span className="game-money">
      {formatCredits(value)} <span className="game-money__unit">{text('common.credits')}</span>
    </span>
  );
}

function FishingRules({ open, onClose }: { readonly open: boolean; readonly onClose: () => void }) {
  const { text } = useGameCopy();
  const sections: readonly GameRulesSection[] = [
    {
      title: text('fishing.rules.chooseTitle'),
      paragraphs: [text('fishing.rules.chooseBody')],
    },
    {
      title: text('fishing.rules.batchTitle'),
      paragraphs: [text('fishing.rules.batchBody')],
    },
    {
      title: text('fishing.rules.resultTitle'),
      paragraphs: [text('fishing.rules.resultBody')],
    },
    {
      title: text('fishing.rules.recoveryTitle'),
      paragraphs: [text('fishing.rules.recoveryBody')],
    },
    {
      title: text('fishing.rules.scoresTitle'),
      paragraphs: [text('fishing.rules.scoresBody')],
    },
    {
      title: text('fishing.rules.startTitle'),
      paragraphs: [text('fishing.rules.startBody')],
    },
  ];
  return (
    <GameRulesDialog
      open={open}
      title={text('fishing.rules.title')}
      closeLabel={text('common.closeRules')}
      sections={sections}
      onClose={onClose}
    />
  );
}

function FishingStage({ phase, level4 }: { readonly phase: string; readonly level4: boolean }) {
  const { text } = useGameCopy();
  return (
    <div className={`fishing-stage${level4 ? ' fishing-stage--l4' : ''}`} data-phase={phase}>
      <svg
        className="fishing-stage__scene"
        viewBox="0 0 800 360"
        role="img"
        aria-label={text(`fishing.stage.${phase}` as Parameters<typeof text>[0])}
      >
        <defs>
          <linearGradient id="fishing-water" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0" stopColor="currentColor" stopOpacity=".18" />
            <stop offset="1" stopColor="currentColor" stopOpacity=".52" />
          </linearGradient>
        </defs>
        <path className="fishing-stage__bank" d="M0 100 Q170 66 340 98T800 78V0H0Z" />
        <path
          className="fishing-stage__water"
          d="M0 105 Q175 77 350 108T800 91V360H0Z"
          fill="url(#fishing-water)"
        />
        <path className="fishing-stage__rod" d="M83 78 Q280 42 560 112" />
        <path className="fishing-stage__line" d="M560 112 Q596 164 607 258" />
        <g className="fishing-stage__float" transform="translate(607 258)">
          <path d="M0-19V-5" />
          <ellipse cx="0" cy="6" rx="9" ry="17" />
          <path d="M-8 4H8" />
        </g>
        <g className="fishing-stage__ripples">
          <ellipse cx="607" cy="279" rx="40" ry="9" />
          <ellipse cx="607" cy="279" rx="76" ry="16" />
        </g>
        <g className="fishing-stage__particles" aria-hidden="true">
          <circle cx="146" cy="184" r="3" />
          <circle cx="246" cy="270" r="2" />
          <circle cx="711" cy="190" r="3" />
        </g>
      </svg>
      <strong className="fishing-stage__label" role="status">
        {text(`fishing.stage.${phase}` as Parameters<typeof text>[0])}
      </strong>
    </div>
  );
}

function ResultPanel({
  result,
  revealed,
  hasMore,
  ackState,
  onRetryACK,
  resultRef,
}: {
  readonly result: FishingBatchResult;
  readonly revealed: number;
  readonly hasMore: boolean;
  readonly ackState: 'idle' | 'sending' | 'failed';
  readonly onRetryACK: () => void;
  readonly resultRef: React.RefObject<HTMLElement | null>;
}) {
  const { language, text } = useGameCopy();
  return (
    <Card className="fishing-result">
      <section ref={resultRef} aria-live="polite" data-batch-id={result.batchID}>
        <div className="fishing-section-heading">
          <div>
            <p className="eyebrow">{text('fishing.result.title')}</p>
            <h2>{text('fishing.result.total')}</h2>
          </div>
          <span>
            {revealed} / {result.outcomes.length}
          </span>
        </div>
        <div className="fishing-outcomes" role="list">
          {result.outcomes.slice(0, revealed).map((outcome) => (
            <article
              className={`fishing-outcome fishing-outcome--${outcome.tier}`}
              key={outcome.ordinal}
              role="listitem"
              data-ordinal={outcome.ordinal}
            >
              <FishingArtwork
                itemKey={outcome.speciesKey}
                label={fishingItemName(outcome.speciesKey, language)}
              />
              <div>
                <strong>{fishingItemName(outcome.speciesKey, language)}</strong>
                <span>
                  {outcome.sizeCM > 0
                    ? text('fishing.size', { size: outcome.sizeCM })
                    : text(
                        `fishing.tier.${outcome.tier === 'legend' ? 'legendary' : outcome.tier === 'junk' || outcome.tier === 'treasure' ? outcome.tier : outcome.tier === 'small' || outcome.tier === 'regular' ? 'common' : 'rare'}` as Parameters<
                          typeof text
                        >[0],
                      )}
                </span>
                <span>{text('fishing.reward', { amount: formatCredits(outcome.reward) })}</span>
              </div>
            </article>
          ))}
        </div>
        {revealed === result.outcomes.length ? (
          <dl className="fishing-totals">
            <div>
              <dt>{text('fishing.result.entry')}</dt>
              <dd>
                <Money value={result.entryTotal} />
              </dd>
            </div>
            <div>
              <dt>{text('fishing.result.payout')}</dt>
              <dd>
                <Money value={result.payoutTotal} />
              </dd>
            </div>
            <div>
              <dt>{text('fishing.result.balance')}</dt>
              <dd>
                <Money value={result.balance} />
              </dd>
            </div>
          </dl>
        ) : null}
        {hasMore && revealed === result.outcomes.length ? (
          <p className="game-inline-notice">{text('fishing.result.more')}</p>
        ) : null}
        {ackState === 'sending' ? <p role="status">{text('fishing.result.ackWaiting')}</p> : null}
        {ackState === 'failed' ? (
          <div className="game-inline-notice game-inline-notice--warning" role="alert">
            <p>{text('fishing.result.ackFailed')}</p>
            <button type="button" className="btn btn-secondary" onClick={onRetryACK}>
              {text('fishing.result.ackRetry')}
            </button>
          </div>
        ) : null}
      </section>
    </Card>
  );
}

function LeaderboardCard({
  board,
  query,
}: {
  readonly board: 'single' | 'total';
  readonly query: {
    readonly data?: FishingLeaderboard;
    readonly error: unknown;
    readonly isPending: boolean;
    readonly refetch: () => unknown;
  };
}) {
  const { language, text } = useGameCopy();
  if (query.isPending)
    return (
      <Card className="fishing-board">
        <LoadingState label={text('common.loading')} />
      </Card>
    );
  if (query.error) {
    return (
      <Card className="fishing-board">
        {isServiceStopped(query.error) ? (
          <p className="game-inline-notice game-inline-notice--warning">
            {text('common.serviceStopped')}
          </p>
        ) : (
          <ErrorState error={query.error} onRetry={() => void query.refetch()} />
        )}
      </Card>
    );
  }
  const data = query.data as Leaderboard | undefined;
  const rows = data?.entries ?? [];
  const missingMe = data?.me && !rows.some((row) => row.isMe) ? [data.me] : [];
  return (
    <Card className="fishing-board">
      <h2>{text(`fishing.leaderboard.${board}`)}</h2>
      <p>
        {text(
          board === 'single' ? 'fishing.leaderboard.helpSingle' : 'fishing.leaderboard.helpTotal',
        )}
      </p>
      {rows.length === 0 && missingMe.length === 0 ? (
        <p>{text('fishing.leaderboard.empty')}</p>
      ) : (
        <div className="fishing-table-wrap">
          <table>
            <thead>
              <tr>
                <th>#</th>
                <th>{text('fishing.leaderboard.angler')}</th>
                <th>
                  {text(
                    board === 'single' ? 'fishing.leaderboard.catch' : 'fishing.leaderboard.score',
                  )}
                </th>
              </tr>
            </thead>
            <tbody>
              {[...rows, ...missingMe].map((row) => {
                const identity =
                  row.identity.kind === 'public'
                    ? row.identity.displayName
                    : text('fishing.leaderboard.anonymous');
                const score =
                  board === 'single'
                    ? `${fishingItemName((row as FishingSingleRow).speciesKey, language)} · ${(row as FishingSingleRow).sizeCM} cm`
                    : `${formatCredits((row as FishingTotalRow).totalCredits)} ${text('common.credits')}`;
                return (
                  <tr
                    key={`${row.rank}-${row.isMe ? 'me' : 'row'}`}
                    className={row.isMe ? 'is-me' : undefined}
                  >
                    <td>{row.rank}</td>
                    <td>
                      {identity}
                      {row.isMe ? ` · ${text('fishing.leaderboard.me')}` : ''}
                    </td>
                    <td>{score}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  );
}

export function FishingGame() {
  const { text } = useGameCopy();
  const queryClient = useQueryClient();
  const snapshot = useGamesSnapshot();
  const maintenance = isMaintenance(snapshot.error);
  const state = useFishingState(Boolean(snapshot.data) && !maintenance);
  const runtimeMaintenance = maintenance || isMaintenance(state.error);
  const single = useFishingLeaderboard('single', Boolean(snapshot.data) && !maintenance);
  const total = useFishingLeaderboard('total', Boolean(snapshot.data) && !maintenance);
  const session = useUserSession(false);
  const reducedMotion = useReducedMotion();
  const [selectedBait, setSelectedBait] = useState<Bait>('worm');
  const [count, setCount] = useState<1 | 10>(1);
  const [operation, setOperation] = useState<FishingStartIntent | null>(null);
  const [actionState, setActionState] = useState<'idle' | 'sending' | 'unknown'>('idle');
  const [actionError, setActionError] = useState<unknown>(null);
  const [recoverKeys, setRecoverKeys] = useState<Readonly<Record<string, string>>>({});
  const [revealClock, setRevealClock] = useState<{
    readonly batchID: string;
    readonly count: number;
  } | null>(null);
  const [ackStatus, setAckStatus] = useState<{
    readonly batchID: string;
    readonly state: 'sending' | 'failed';
  } | null>(null);
  const [sound, setSound] = useState(false);
  const [rulesOpen, setRulesOpen] = useState(false);
  const audioRef = useRef<AudioContext | null>(null);
  const resultRef = useRef<HTMLElement | null>(null);
  const ackAttempted = useRef<string | null>(null);
  const authoritative = state.data;
  const refetchState = state.refetch;
  const result = authoritative?.unrevealed ?? null;
  const pending = authoritative?.settlementPending ?? null;
  const authorityResolvedStart = Boolean(operation && (pending || result));
  const effectiveActionState = authorityResolvedStart ? 'idle' : actionState;
  const replayOperation = authorityResolvedStart ? null : operation;
  const revealed = result
    ? reducedMotion
      ? result.outcomes.length
      : revealClock?.batchID === result.batchID
        ? revealClock.count
        : 0
    : 0;
  const ackState =
    result && ackStatus?.batchID === result.batchID ? ackStatus.state : ('idle' as const);

  useEffect(
    () => () => {
      void audioRef.current?.close();
    },
    [],
  );
  useEffect(() => {
    if (!result || reducedMotion) return undefined;
    const timer = window.setInterval(() => {
      setRevealClock((current) => {
        const count = current?.batchID === result.batchID ? current.count : 0;
        const next = nextRevealCount(count, result);
        return next === count && current?.batchID === result.batchID
          ? current
          : { batchID: result.batchID, count: next };
      });
    }, FISHING_REVEAL_MS);
    return () => window.clearInterval(timer);
  }, [reducedMotion, result]);

  useEffect(() => {
    if (!sound || revealed === 0 || !audioRef.current) return;
    const context = audioRef.current;
    const oscillator = context.createOscillator();
    const gain = context.createGain();
    oscillator.frequency.value = 420 + revealed * 24;
    gain.gain.setValueAtTime(0.035, context.currentTime);
    gain.gain.exponentialRampToValueAtTime(0.001, context.currentTime + 0.09);
    oscillator.connect(gain).connect(context.destination);
    oscillator.start();
    oscillator.stop(context.currentTime + 0.1);
  }, [revealed, sound]);

  const refreshAfterMutation = useCallback(async () => {
    await queryClient.invalidateQueries({ queryKey: fishingKeys.state });
    await queryClient.invalidateQueries({ queryKey: gameKeys.snapshot });
  }, [queryClient]);

  const sendACK = useCallback(
    async (batchID: string) => {
      setAckStatus({ batchID, state: 'sending' });
      try {
        await acknowledgeFishing(batchID);
        setAckStatus(null);
        await refreshAfterMutation();
        await queryClient.invalidateQueries({
          queryKey: ['user', 'games', 'fishing', 'leaderboard'],
        });
      } catch {
        setAckStatus({ batchID, state: 'failed' });
        await refetchState();
      }
    },
    [queryClient, refetchState, refreshAfterMutation],
  );

  useEffect(() => {
    if (!result || revealed !== result.outcomes.length || ackAttempted.current === result.batchID)
      return;
    if (resultRef.current?.querySelectorAll('[data-ordinal]').length !== result.outcomes.length)
      return;
    ackAttempted.current = result.batchID;
    let second = 0;
    const first = requestAnimationFrame(() => {
      second = requestAnimationFrame(() => void sendACK(result.batchID));
    });
    return () => {
      cancelAnimationFrame(first);
      if (second) cancelAnimationFrame(second);
    };
  }, [result, revealed, sendACK]);

  const adoptStart = useCallback(
    async (intent: FishingStartIntent) => {
      setActionState('sending');
      setActionError(null);
      try {
        const response = await startFishing(intent);
        setOperation(null);
        setActionState('idle');
        queryClient.setQueryData(
          fishingKeys.state,
          isFishingResult(response)
            ? { settlementPending: null, unrevealed: response, hasMoreUnrevealed: false }
            : { settlementPending: response, unrevealed: null, hasMoreUnrevealed: false },
        );
        await refreshAfterMutation();
      } catch (error) {
        setActionError(error);
        if (isResponseUnknown(error)) setActionState('unknown');
        else {
          setOperation(null);
          setActionState('idle');
          if (isConflict(error)) await refetchState();
        }
      }
    },
    [queryClient, refetchState, refreshAfterMutation],
  );

  const start = () => {
    const intent = operation ?? {
      bait: selectedBait,
      count,
      idempotencyKey: createIdempotencyKey(),
    };
    setOperation(intent);
    void adoptStart(intent);
  };

  const recover = async () => {
    if (!pending || pending.state !== 'recovery_required') return;
    setOperation(null);
    const key = recoverKeys[pending.batchID] ?? createIdempotencyKey();
    if (!recoverKeys[pending.batchID])
      setRecoverKeys((value) => ({ ...value, [pending.batchID]: key }));
    setActionState('sending');
    setActionError(null);
    try {
      const response = await recoverFishing(pending.batchID, key);
      queryClient.setQueryData(
        fishingKeys.state,
        isFishingResult(response)
          ? { settlementPending: null, unrevealed: response, hasMoreUnrevealed: false }
          : { settlementPending: response, unrevealed: null, hasMoreUnrevealed: false },
      );
      setRecoverKeys((value) => {
        const next = { ...value };
        delete next[pending.batchID];
        return next;
      });
      setOperation(null);
      setActionState('idle');
      await refreshAfterMutation();
    } catch (error) {
      setActionError(error);
      const unknown = isResponseUnknown(error);
      setActionState(unknown ? 'unknown' : 'idle');
      if (!unknown)
        setRecoverKeys((value) => {
          const next = { ...value };
          delete next[pending.batchID];
          return next;
        });
      if (isConflict(error)) await refetchState();
    }
  };

  const toggleSound = () => {
    if (!sound) {
      audioRef.current ??= new AudioContext();
      void audioRef.current.resume();
    }
    setSound((value) => !value);
  };

  const prices = snapshot.data?.fishing.baitPrices;
  const unit = prices?.[selectedBait] ?? '0';
  const frozenTotal = multiplyCredits(unit, count);
  const affordable = snapshot.data
    ? creditsToMilli(snapshot.data.balance, true) >= creditsToMilli(frozenTotal)
    : false;
  const startsOpen = Boolean(
    snapshot.data?.gamesEnabled && snapshot.data.fishing.enabled && snapshot.data.fishing.available,
  );
  const locked = Boolean(
    state.isPending ||
    state.error ||
    pending ||
    replayOperation ||
    effectiveActionState !== 'idle',
  );
  const phase = fishingPresentationPhase({
    submitting: effectiveActionState === 'sending' && !pending,
    pending: Boolean(pending),
    result,
    revealed,
  });
  const closeRules = useCallback(() => setRulesOpen(false), []);
  const rulesButton = (
    <GameRulesButton label={text('common.rulesButton')} onClick={() => setRulesOpen(true)} />
  );
  const rulesDialog = <FishingRules open={rulesOpen} onClose={closeRules} />;

  if (snapshot.isPending)
    return (
      <main className="game-page fishing-page">
        <PageHeader
          back={
            <Link className="game-back-link" to="/games">
              {text('common.back')}
            </Link>
          }
          eyebrow={text('fishing.eyebrow')}
          title={text('fishing.title')}
          description={text('fishing.description')}
          actions={rulesButton}
        />
        <LoadingState label={text('common.loading')} />
        {rulesDialog}
      </main>
    );
  if (snapshot.error && !maintenance)
    return (
      <main className="game-page fishing-page">
        <PageHeader
          back={
            <Link className="game-back-link" to="/games">
              {text('common.back')}
            </Link>
          }
          eyebrow={text('fishing.eyebrow')}
          title={text('fishing.title')}
          description={text('fishing.description')}
          actions={rulesButton}
        />
        <ErrorState error={snapshot.error} onRetry={() => void snapshot.refetch()} />
        {rulesDialog}
      </main>
    );
  if (runtimeMaintenance || !snapshot.data)
    return (
      <main className="game-page fishing-page">
        <PageHeader
          back={
            <Link className="game-back-link" to="/games">
              {text('common.back')}
            </Link>
          }
          eyebrow={text('fishing.eyebrow')}
          title={text('fishing.title')}
          description={text('fishing.description')}
          actions={rulesButton}
        />
        <p className="game-inline-notice game-inline-notice--warning" role="status">
          {text('common.maintenance')}
        </p>
        {rulesDialog}
      </main>
    );

  return (
    <main className="game-page fishing-page">
      <PageHeader
        back={
          <Link className="game-back-link" to="/games">
            {text('common.back')}
          </Link>
        }
        eyebrow={text('fishing.eyebrow')}
        title={text('fishing.title')}
        description={text('fishing.description')}
        actions={
          <>
            {rulesButton}
            <button
              type="button"
              className="btn btn-secondary"
              aria-pressed={sound}
              onClick={toggleSound}
            >
              {text(sound ? 'fishing.sound.on' : 'fishing.sound.off')}
            </button>
          </>
        }
      />
      <span className="sr-only">{text('fishing.sound.hint')}</span>
      <div className="fishing-layout">
        <div className="fishing-main">
          <FishingStage phase={phase} level4={(session.data?.user.effective_level ?? 0) >= 4} />
          {state.isPending ? <LoadingState label={text('common.loading')} /> : null}
          {state.error ? (
            <ErrorState error={state.error} onRetry={() => void state.refetch()} />
          ) : null}
          {pending ? (
            <Card className="fishing-pending">
              <h2>
                {text(
                  pending.state === 'recovery_required'
                    ? 'fishing.recovery.title'
                    : 'fishing.pending.title',
                )}
              </h2>
              <p>
                {text(
                  pending.state === 'recovery_required'
                    ? 'fishing.recovery.body'
                    : 'fishing.pending.body',
                )}
              </p>
              <dl className="fishing-totals">
                <div>
                  <dt>{text('fishing.totalPrice')}</dt>
                  <dd>
                    <Money value={pending.entryTotal} />
                  </dd>
                </div>
              </dl>
              {pending.nextAttemptAt !== null ? (
                <p>
                  {text('fishing.pending.next', {
                    time: new Date(pending.nextAttemptAt * 1000).toLocaleTimeString(),
                  })}
                </p>
              ) : null}
              {pending.state === 'recovery_required' ? (
                <button
                  type="button"
                  className="btn btn-primary"
                  disabled={actionState !== 'idle'}
                  onClick={() => void recover()}
                >
                  {text(
                    actionState === 'sending'
                      ? 'fishing.recovery.running'
                      : 'fishing.recovery.action',
                  )}
                </button>
              ) : null}
            </Card>
          ) : null}
          {result ? (
            <ResultPanel
              result={result}
              revealed={revealed}
              hasMore={authoritative?.hasMoreUnrevealed ?? false}
              ackState={ackState}
              onRetryACK={() => {
                ackAttempted.current = result.batchID;
                void sendACK(result.batchID);
              }}
              resultRef={resultRef}
            />
          ) : null}
          {effectiveActionState === 'unknown' ? (
            <div className="game-inline-notice game-inline-notice--warning" role="alert">
              <p>{text('common.responseUnknown')}</p>
              <div className="game-state-actions">
                <button
                  type="button"
                  className="btn btn-secondary"
                  onClick={() => void state.refetch()}
                >
                  {text('fishing.unknown.check')}
                </button>
                {replayOperation ? (
                  <button
                    type="button"
                    className="btn btn-primary"
                    onClick={() => void adoptStart(replayOperation)}
                  >
                    {text('fishing.unknown.replay')}
                  </button>
                ) : null}
                {pending?.state === 'recovery_required' ? (
                  <button type="button" className="btn btn-primary" onClick={() => void recover()}>
                    {text('fishing.recovery.action')}
                  </button>
                ) : null}
              </div>
            </div>
          ) : null}
          {actionError && effectiveActionState !== 'unknown' ? (
            <ErrorState error={actionError} onRetry={() => void state.refetch()} />
          ) : null}
        </div>
        <Card className="fishing-controls">
          <h2>{text('fishing.start')}</h2>
          {!startsOpen ? (
            <p className="game-inline-notice game-inline-notice--warning">
              {text(
                snapshot.data.fishing.enabled ? 'fishing.runtimeUnavailable' : 'fishing.closed',
              )}
            </p>
          ) : null}
          <fieldset disabled={locked || !startsOpen}>
            <legend>{text('fishing.selected')}</legend>
            <div className="fishing-choice-grid">
              {BAITS.map((bait) => (
                <button
                  type="button"
                  className={bait === selectedBait ? 'is-selected' : ''}
                  aria-pressed={bait === selectedBait}
                  key={bait}
                  onClick={() => setSelectedBait(bait)}
                >
                  <strong>{text(`fishing.bait.${bait}`)}</strong>
                  <Money value={prices?.[bait] ?? '0'} />
                </button>
              ))}
            </div>
          </fieldset>
          <fieldset disabled={locked || !startsOpen}>
            <legend>{text('fishing.result.total')}</legend>
            <div className="fishing-count">
              <button
                type="button"
                className={count === 1 ? 'is-selected' : ''}
                aria-pressed={count === 1}
                onClick={() => setCount(1)}
              >
                {text('fishing.batch.one')}
              </button>
              <button
                type="button"
                className={count === 10 ? 'is-selected' : ''}
                aria-pressed={count === 10}
                onClick={() => setCount(10)}
              >
                {text('fishing.batch.ten')}
              </button>
            </div>
          </fieldset>
          <dl className="fishing-totals">
            <div>
              <dt>{text('fishing.unitPrice')}</dt>
              <dd>
                <Money value={unit} />
              </dd>
            </div>
            <div>
              <dt>{text('fishing.totalPrice')}</dt>
              <dd>
                <Money value={frozenTotal} />
              </dd>
            </div>
            <div>
              <dt>{text('fishing.balance')}</dt>
              <dd>
                <Money value={snapshot.data.balance} />
              </dd>
            </div>
          </dl>
          {!affordable ? (
            <p className="game-inline-notice game-inline-notice--danger">
              {text('fishing.insufficient')}
            </p>
          ) : null}
          <button
            type="button"
            className="btn btn-primary fishing-start"
            disabled={!startsOpen || locked || !affordable}
            onClick={start}
          >
            {text(effectiveActionState === 'sending' ? 'fishing.starting' : 'fishing.start')}
          </button>
        </Card>
      </div>
      <section className="fishing-leaderboards" aria-label="Fishing leaderboards">
        <LeaderboardCard board="single" query={single} />
        <LeaderboardCard board="total" query={total} />
      </section>
      <p className="game-footnote">{text('common.noHistory')}</p>
      {rulesDialog}
    </main>
  );
}
