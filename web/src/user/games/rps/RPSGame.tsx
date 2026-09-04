import { useCallback, useEffect, useRef, useState } from 'react';
import { Link } from 'react-router';
import { useQueryClient } from '@tanstack/react-query';
import { Card, ErrorState, LoadingState, PageHeader, StatusBadge } from '@shared/components/States';
import { useGameCopy } from '../copy';
import { GameRulesButton, GameRulesDialog, type GameRulesSection } from '../common/GameRulesDialog';
import {
  createIdempotencyKey,
  createOpaqueID,
  isConflict,
  isMaintenance,
  isResponseUnknown,
  isServiceStopped,
} from '../common/request';
import { creditsToMilli, formatCredits, multiplyCredits } from '../common/strict';
import { RPS_MODES, type RPSMode } from '../common/types';
import { gameKeys, useGamesSnapshot } from '../common/snapshot';
import { useAuthoritativeCountdown } from '../common/countdown';
import {
  acknowledgeRPSResult,
  cancelRPSQueue,
  enqueueRPS,
  markRPSTutorialSeen,
  renewRPSLease,
  rpsKeys,
  sendRPSAction,
  useRPSLeaderboard,
  useRPSState,
} from './api';
import { rpsDeviceToken } from './device';
import { shouldApplyRPSReplacement } from './normalize';
import { connectRPSStream } from './stream';
import { GestureArt } from './GestureArt';
import {
  GESTURES,
  type RPSActionIntent,
  type RPSActionPayload,
  type RPSHomeState,
  type RPSLeaderboard,
  type RPSPendingResult,
  type RPSSeat,
  type RPSState,
} from './types';
import './rps.css';

type Operation =
  | {
      readonly kind: 'queue';
      readonly mode: RPSMode;
      readonly token: string;
      readonly confirmed: boolean;
      readonly key: string;
    }
  | {
      readonly kind: 'cancel';
      readonly queueID: string;
      readonly revision: string;
      readonly key: string;
    }
  | { readonly kind: 'action'; readonly intent: RPSActionIntent };

function Money({ value }: { readonly value: string }) {
  const { text } = useGameCopy();
  return (
    <span className="game-money">
      {formatCredits(value)} <span className="game-money__unit">{text('common.credits')}</span>
    </span>
  );
}

function RPSRules({ open, onClose }: { readonly open: boolean; readonly onClose: () => void }) {
  const { text } = useGameCopy();
  const sections: readonly GameRulesSection[] = [
    {
      title: text('rps.rules.goalTitle'),
      paragraphs: [text('rps.rules.goalBody')],
    },
    {
      title: text('rps.rules.roundTitle'),
      paragraphs: [text('rps.rules.roundBody')],
    },
    {
      title: text('rps.rules.scoreTitle'),
      items: [
        text('rps.rules.score.same'),
        text('rps.rules.score.oneWinner'),
        text('rps.rules.score.twoWinners'),
      ],
    },
    {
      title: text('rps.rules.modesTitle'),
      items: [
        text('rps.rules.mode.quick'),
        text('rps.rules.mode.standard'),
        text('rps.rules.mode.deathmatch'),
      ],
    },
    {
      title: text('rps.rules.feesTitle'),
      paragraphs: [text('rps.rules.feesBody')],
    },
    {
      title: text('rps.rules.dealerTitle'),
      paragraphs: [text('rps.rules.dealerBody'), text('rps.rules.surrenderBody')],
    },
    {
      title: text('rps.rules.poolTitle'),
      paragraphs: [text('rps.rules.poolBody')],
    },
    {
      title: text('rps.rules.ultimateTitle'),
      paragraphs: [text('rps.rules.ultimateBody')],
    },
    {
      title: text('rps.rules.queueTitle'),
      paragraphs: [text('rps.rules.queueBody')],
    },
    {
      title: text('rps.rules.statsTitle'),
      paragraphs: [text('rps.rules.statsBody')],
    },
    {
      title: text('rps.rules.resultTitle'),
      paragraphs: [text('rps.rules.resultBody')],
    },
  ];
  return (
    <GameRulesDialog
      open={open}
      title={text('rps.rules.title')}
      closeLabel={text('common.closeRules')}
      sections={sections}
      onClose={onClose}
    />
  );
}

function queueCommitment(mode: RPSMode, base: string, balance: string): string {
  if (mode === 'standard') return multiplyCredits(base, 5);
  return mode === 'deathmatch' ? balance : base;
}

function Tutorial({
  page,
  onPage,
  onSkip,
  onFinish,
}: {
  readonly page: number;
  readonly onPage: (page: number) => void;
  readonly onSkip: () => void;
  readonly onFinish: () => void;
}) {
  const { text } = useGameCopy();
  return (
    <div className="rps-modal" role="dialog" aria-modal="true" aria-labelledby="rps-tutorial-title">
      <Card className="rps-tutorial">
        <p className="eyebrow">{page + 1} / 3</p>
        <h2 id="rps-tutorial-title">{text('rps.tutorial.title')}</h2>
        <div className="rps-tutorial__art">
          <GestureArt gesture={GESTURES[page]} />
        </div>
        <p>{text(`rps.tutorial.${page + 1}` as 'rps.tutorial.1')}</p>
        <div className="game-state-actions">
          <button type="button" className="btn btn-secondary" onClick={onSkip}>
            {text('rps.tutorial.skip')}
          </button>
          {page < 2 ? (
            <button type="button" className="btn btn-primary" onClick={() => onPage(page + 1)}>
              {text('rps.tutorial.next')}
            </button>
          ) : (
            <button type="button" className="btn btn-primary" onClick={onFinish}>
              {text('rps.tutorial.finish')}
            </button>
          )}
        </div>
      </Card>
    </div>
  );
}

function percentage(numerator: bigint, denominator: bigint): string {
  if (denominator === 0n) return '0';
  const basisPoints = (numerator * 10_000n) / denominator;
  const whole = basisPoints / 100n;
  const fraction = (basisPoints % 100n).toString().padStart(2, '0').replace(/0+$/, '');
  return `${whole}${fraction ? `.${fraction}` : ''}`;
}

function FunStats({ seat }: { readonly seat: RPSSeat }) {
  const { text } = useGameCopy();
  if (seat.deletionState !== 'active') return null;
  const fun = seat.funSnapshot;
  if (fun.state === 'none') return <p>{text('rps.fun.none')}</p>;
  const count = BigInt(fun.completedCount);
  if (count === 0n) return <p>{text('rps.fun.none')}</p>;
  if (fun.state === 'insufficient' || count < 10n)
    return <p>{text('rps.fun.insufficient', { count: fun.completedCount })}</p>;
  const profitable = BigInt(fun.profitableCount);
  const gestures = [
    ['rock', fun.rockCount],
    ['scissors', fun.scissorsCount],
    ['paper', fun.paperCount],
  ] as const;
  const gestureTotal = gestures.reduce((total, [, value]) => total + BigInt(value), 0n);
  return (
    <div className="rps-fun">
      <p>
        {text('rps.fun.full', {
          count: fun.completedCount,
          rate: percentage(profitable, count),
        })}
      </p>
      <div>
        {gestures.map(([gesture, value]) => (
          <span key={gesture}>
            {text('rps.fun.gesture', {
              gesture: text(`rps.gesture.${gesture}`),
              count: value,
              rate: percentage(BigInt(value), gestureTotal),
            })}
          </span>
        ))}
      </div>
      <p>{text('rps.fun.managed')}</p>
    </div>
  );
}

function SeatCard({ seat }: { readonly seat: RPSSeat }) {
  const { text } = useGameCopy();
  const name = seat.deletionState === 'active' ? seat.displayName : text('rps.seat.deleted');
  return (
    <Card className={`rps-seat${seat.viewer === 'self' ? ' is-self' : ''}`}>
      <div className="rps-seat__head">
        {seat.deletionState === 'active' && seat.avatarURL ? (
          <img src={seat.avatarURL} alt="" width="40" height="40" referrerPolicy="no-referrer" />
        ) : (
          <span className="rps-seat__avatar" aria-hidden="true">
            {seat.seatNo + 1}
          </span>
        )}
        <div>
          <strong>{name}</strong>
          <span>{seat.viewer === 'self' ? ` · ${text('rps.leaderboard.me')}` : ''}</span>
        </div>
      </div>
      <dl>
        <div>
          <dt>{text('rps.seat.balance')}</dt>
          <dd>
            <Money value={seat.currentBalance} />
          </dd>
        </div>
        <div>
          <dt>{text('rps.seat.input')}</dt>
          <dd>
            <Money value={seat.currentRoundInput} />
          </dd>
        </div>
      </dl>
      {seat.currentAllIn ? <strong>{text('rps.seat.allIn')}</strong> : null}
      {BigInt(seat.timeoutCount) > 0n ? (
        <p className="game-inline-notice">
          {text('rps.seat.managed', { count: seat.timeoutCount })}
        </p>
      ) : null}
      <div className="rps-seat__gesture">
        {seat.deletionState === 'active' && seat.visibleGesture ? (
          <>
            <GestureArt gesture={seat.visibleGesture} />
            <span>{text(`rps.gesture.${seat.visibleGesture}`)}</span>
          </>
        ) : (
          <span>{text('rps.gesture.hidden')}</span>
        )}
      </div>
      <FunStats seat={seat} />
    </Card>
  );
}

function ActionControls({
  state,
  busy,
  onAction,
}: {
  readonly state: RPSState;
  readonly busy: boolean;
  readonly onAction: (value: RPSActionPayload) => void;
}) {
  const { text } = useGameCopy();
  const option = state.currentActorOptions[0];
  const [raise, setRaise] = useState('1');
  const maximumMilli =
    state.seats.reduce(
      (minimum, seat) => {
        const value = creditsToMilli(seat.currentBalance);
        return minimum === null || value < minimum ? value : minimum;
      },
      null as bigint | null,
    ) ?? 0n;
  const maximumWhole = maximumMilli / 1000n;
  const raiseValid =
    raise.length <= 75 && /^(?:[1-9][0-9]*)$/.test(raise) && BigInt(raise) <= maximumWhole;
  if (!option) return <p className="game-inline-notice">{text('rps.waiting')}</p>;
  if (option === 'gesture')
    return (
      <div className="rps-actions" aria-label={text(`rps.phase.${state.phase}`)}>
        {GESTURES.map((gesture) => (
          <button
            type="button"
            key={gesture}
            disabled={busy}
            onClick={() => onAction({ action: 'gesture', payload: { gesture } })}
          >
            <GestureArt gesture={gesture} />
            <span>{text(`rps.gesture.${gesture}`)}</span>
          </button>
        ))}
      </div>
    );
  if (option === 'dealer_decision')
    return (
      <div className="rps-dealer">
        <p>
          {maximumWhole > 0n
            ? text('rps.dealer.integerOnly', { max: maximumWhole.toString() })
            : text('rps.dealer.noCapacity')}
        </p>
        <div className="game-state-actions">
          <button
            type="button"
            className="btn btn-secondary"
            disabled={busy}
            onClick={() =>
              onAction({ action: 'dealer_decision', payload: { decision: 'no_raise' } })
            }
          >
            {text('rps.dealer.noRaise')}
          </button>
          {maximumWhole > 0n ? (
            <>
              <label>
                <span>{text('rps.raiseAmount')}</span>
                <input
                  value={raise}
                  inputMode="numeric"
                  pattern="[1-9][0-9]*"
                  maxLength={75}
                  onChange={(event) => setRaise(event.target.value)}
                />
              </label>
              <button
                type="button"
                className="btn btn-primary"
                disabled={busy || !raiseValid}
                onClick={() =>
                  onAction({
                    action: 'dealer_decision',
                    payload: { decision: 'raise', amount: raise },
                  })
                }
              >
                {text('rps.dealer.raise')}
              </button>
            </>
          ) : null}
        </div>
      </div>
    );
  return (
    <div className="game-state-actions">
      <button
        type="button"
        className="btn btn-primary"
        disabled={busy}
        onClick={() => onAction({ action: 'follower_decision', payload: { decision: 'call' } })}
      >
        {text('rps.follower.call')}
      </button>
      <button
        type="button"
        className="btn btn-secondary"
        disabled={busy}
        onClick={() =>
          onAction({ action: 'follower_decision', payload: { decision: 'surrender' } })
        }
      >
        {text('rps.follower.surrender')}
      </button>
    </div>
  );
}

function Match({
  state,
  lease,
  stream,
  busy,
  onAction,
  onRefresh,
}: {
  readonly state: RPSState;
  readonly lease: string;
  readonly stream: string;
  readonly busy: boolean;
  readonly onAction: (value: RPSActionPayload) => void;
  readonly onRefresh: () => void;
}) {
  const { text } = useGameCopy();
  const remaining = useAuthoritativeCountdown(
    `${state.phaseSeq}:${state.revision}`,
    state.deadline,
    state.serverNow,
    onRefresh,
  );
  return (
    <section className="rps-match">
      <Card className="rps-match__status">
        <div className="rps-match__heading">
          <div>
            <p className="eyebrow">{text('rps.match.title')}</p>
            <h2>{text(`rps.phase.${state.phase}`)}</h2>
          </div>
          <div className="rps-connection">
            <StatusBadge
              active={stream === 'connected'}
              danger={stream === 'disconnected'}
              label={text(`rps.stream.${stream}` as 'rps.stream.connected')}
            />
            <StatusBadge
              active={lease === 'active'}
              danger={lease === 'lost' || lease === 'stopped'}
              label={
                lease === 'active'
                  ? text('common.leaseActive')
                  : lease === 'renewing'
                    ? text('common.leaseRenewing')
                    : lease === 'stopped'
                      ? text('common.serviceStopped')
                      : text('common.leaseLost')
              }
            />
          </div>
        </div>
        {state.deadline !== null ? (
          <p>
            <strong>{text('rps.phaseDeadline')}:</strong>{' '}
            {new Date(state.deadline * 1000).toLocaleTimeString()} · {remaining ?? 0}s
          </p>
        ) : null}
        {state.roundSummary.reminderActive ? (
          <p className="game-inline-notice game-inline-notice--warning">{text('rps.reminder')}</p>
        ) : null}
        {state.phase === 'ultimate_gesture' ? (
          <p className="game-inline-notice game-inline-notice--warning">{text('rps.ultimate')}</p>
        ) : null}
      </Card>
      <div className="rps-seats">
        {state.seats.map((seat) => (
          <SeatCard key={seat.seatNo} seat={seat} />
        ))}
      </div>
      <Card className="rps-economy">
        <dl>
          <div>
            <dt>{text('rps.base')}</dt>
            <dd>
              <Money value={state.ruleSnapshot.base} />
            </dd>
          </div>
          <div>
            <dt>{text('rps.pool')}</dt>
            <dd>
              <Money value={state.economy.playerPool} />
            </dd>
          </div>
          <div>
            <dt>{text('rps.permanentMultiplier')}</dt>
            <dd>{state.economy.permanentMultiplier}×</dd>
          </div>
          {state.economy.currentPlanMultiplier ? (
            <div>
              <dt>{text('rps.planMultiplier')}</dt>
              <dd>{state.economy.currentPlanMultiplier}×</dd>
            </div>
          ) : null}
          {state.economy.poolBaseMultiplier ? (
            <div>
              <dt>{text('rps.poolMultiplier')}</dt>
              <dd>{state.economy.poolBaseMultiplier}×</dd>
            </div>
          ) : null}
          {state.economy.dealerRaise ? (
            <div>
              <dt>{text('rps.raiseAmount')}</dt>
              <dd>
                <Money value={state.economy.dealerRaise} />
              </dd>
            </div>
          ) : null}
          <div>
            <dt>{text('rps.baseRounds')}</dt>
            <dd>{state.roundSummary.baseRoundCount}</dd>
          </div>
          <div>
            <dt>{text('rps.paidTies')}</dt>
            <dd>{state.roundSummary.paidTieCount}</dd>
          </div>
          <div>
            <dt>{text('rps.freeTies', { limit: state.ruleSnapshot.freeTieLimit })}</dt>
            <dd>{state.roundSummary.freeTieCount}</dd>
          </div>
          <div>
            <dt>{text('rps.poolStreaks')}</dt>
            <dd>
              {text('rps.poolStreakValues', {
                paid: state.roundSummary.paidPoolStreak,
                free: state.roundSummary.freePoolStreak,
              })}
            </dd>
          </div>
        </dl>
        <p>
          {text('rps.cuts', {
            platform: formatCredits(state.economy.cuts.platform),
            welfare: formatCredits(state.economy.cuts.welfare),
            thursday: formatCredits(state.economy.cuts.thursday),
          })}
        </p>
        <p>
          {text('rps.welfareCarry', {
            amount: formatCredits(state.economy.welfareCarry),
          })}
        </p>
      </Card>
      <Card className="rps-action-panel">
        <h2>{text(`rps.phase.${state.phase}`)}</h2>
        <ActionControls state={state} busy={busy} onAction={onAction} />
        {busy ? <p role="status">{text('rps.actionSending')}</p> : null}
      </Card>
    </section>
  );
}

function PendingResult({
  result,
  ack,
  onACK,
  resultRef,
}: {
  readonly result: RPSPendingResult;
  readonly ack: 'idle' | 'sending' | 'failed';
  readonly onACK: () => void;
  readonly resultRef: React.RefObject<HTMLElement | null>;
}) {
  const { text } = useGameCopy();
  return (
    <Card className="rps-result">
      <section ref={resultRef} data-session-id={result.sessionID}>
        <p className="eyebrow">{text('rps.result.title')}</p>
        <h2>{text(`rps.mode.${result.mode}`)}</h2>
        <dl>
          <div>
            <dt>{text('rps.result.reason')}</dt>
            <dd>{text(`rps.result.reason.${result.terminalReason}`)}</dd>
          </div>
          <div>
            <dt>{text('rps.result.input')}</dt>
            <dd>
              <Money value={result.ownInput} />
            </dd>
          </div>
          <div>
            <dt>{text('rps.result.returned')}</dt>
            <dd>
              <Money value={result.ownReturned} />
            </dd>
          </div>
          <div>
            <dt>{text('rps.result.net')}</dt>
            <dd>
              <Money value={result.ownWalletNet} />
            </dd>
          </div>
        </dl>
        <div className="rps-result__seats" role="list">
          {result.seats.map((seat) => (
            <div role="listitem" data-result-seat={seat.seatNo} key={seat.seatNo}>
              <strong>
                {seat.seatNo === result.ownSeatNo
                  ? text('rps.leaderboard.me')
                  : `#${seat.seatNo + 1}`}
              </strong>
              <span>
                {seat.result === 'deidentified'
                  ? text('rps.result.deidentified')
                  : text(`rps.result.${seat.result}`)}
              </span>
            </div>
          ))}
        </div>
        {ack === 'sending' ? <p role="status">{text('rps.result.ackWaiting')}</p> : null}
        {ack === 'failed' ? (
          <div className="game-inline-notice game-inline-notice--warning" role="alert">
            <p>{text('rps.result.ackFailed')}</p>
            <button type="button" className="btn btn-primary" onClick={onACK}>
              {text('rps.result.ackRetry')}
            </button>
          </div>
        ) : null}
      </section>
    </Card>
  );
}

function Leaderboard({
  data,
  board,
}: {
  readonly data: RPSLeaderboard | undefined;
  readonly board: 'profit_rate' | 'net_profit';
}) {
  const { text } = useGameCopy();
  const rows = data ? [...data.rows, ...(data.me ? [data.me] : [])] : [];
  return (
    <Card className="rps-board">
      <h3>{text(`rps.leaderboard.${board}`)}</h3>
      {rows.length === 0 ? (
        <p>{text('rps.leaderboard.empty')}</p>
      ) : (
        <div className="rps-table-wrap">
          <table>
            <thead>
              <tr>
                <th>#</th>
                <th>{text('rps.leaderboard.player')}</th>
                <th>{text(`rps.leaderboard.${board}`)}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => (
                <tr
                  key={`${row.rank}-${row.isMe ? 'me' : 'row'}`}
                  className={row.isMe ? 'is-me' : undefined}
                >
                  <td>{row.rank}</td>
                  <td>
                    {row.identity.kind === 'public'
                      ? row.identity.displayName
                      : text('rps.leaderboard.anonymous')}
                    {row.isMe ? ` · ${text('rps.leaderboard.me')}` : ''}
                  </td>
                  <td>
                    {row.board === 'profit_rate' ? (
                      `${row.profitRate}% · ${text('rps.leaderboard.sessions', { count: row.sessionCount })}`
                    ) : (
                      <>
                        <Money value={row.netProfit} /> ·{' '}
                        {text('rps.leaderboard.sessions', { count: row.sessionCount })}
                      </>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  );
}

export function RPSGame() {
  const { text } = useGameCopy();
  const queryClient = useQueryClient();
  const snapshot = useGamesSnapshot();
  const maintenance = isMaintenance(snapshot.error);
  const homeQuery = useRPSState(Boolean(snapshot.data));
  const home = homeQuery.data;
  const session = home?.kind === 'session' ? home.session : null;
  const authoritativeMode =
    home?.kind === 'session' ? home.session.mode : home?.kind === 'queue' ? home.queue.mode : null;
  const [selectedMode, setSelectedMode] = useState<RPSMode>('quick');
  const [rulesOpen, setRulesOpen] = useState(false);
  const displayedMode = authoritativeMode ?? selectedMode;
  const [tutorialPage, setTutorialPage] = useState(0);
  const [tutorialVisibility, setTutorialVisibility] = useState<'auto' | 'open' | 'closed'>('auto');
  const [tutorialSaveFailed, setTutorialSaveFailed] = useState(false);
  const [deathmatchReview, setDeathmatchReview] = useState(false);
  const [operation, setOperation] = useState<Operation | null>(null);
  const [operationState, setOperationState] = useState<'idle' | 'sending' | 'unknown'>('idle');
  const [operationError, setOperationError] = useState<unknown>(null);
  const [leaseState, setLeaseState] = useState<{
    readonly sessionID: string;
    readonly value: 'renewing' | 'active' | 'lost' | 'stopped';
  } | null>(null);
  const [stream, setStream] = useState<'connecting' | 'connected' | 'disconnected' | 'gap'>(
    'connecting',
  );
  const [ackStatus, setAckStatus] = useState<{
    readonly sessionID: string;
    readonly value: 'sending' | 'failed';
  } | null>(null);
  const resultRef = useRef<HTMLElement | null>(null);
  const acked = useRef<string | null>(null);
  const cancelledQueue = useRef<string | null>(null);
  const pending = home?.kind === 'pending_result' ? home.result : null;
  const continuingPending = Boolean(
    pending && leaseState?.sessionID === pending.sessionID && leaseState.value === 'active',
  );
  const showPendingResult = Boolean(pending && (!maintenance || continuingPending));
  const tutorialSeen = home?.kind === 'idle' ? home.tutorialSeen : snapshot.data?.tutorialRPSSeen;
  const tutorialOpen =
    tutorialVisibility === 'open' ||
    (tutorialVisibility === 'auto' &&
      Boolean(snapshot.data && !tutorialSeen && home?.kind !== 'pending_result'));
  const activeSessionID = session?.sessionID ?? null;
  const leaseSessionID =
    activeSessionID ?? (continuingPending ? (pending?.sessionID ?? null) : null);
  const lease = activeSessionID
    ? leaseState?.sessionID === activeSessionID
      ? leaseState.value
      : 'renewing'
    : 'none';
  const ack =
    home?.kind === 'pending_result' && ackStatus?.sessionID === home.result.sessionID
      ? ackStatus.value
      : ('idle' as const);
  const profit = useRPSLeaderboard(
    displayedMode,
    'profit_rate',
    Boolean(snapshot.data) && !maintenance,
  );
  const net = useRPSLeaderboard(
    displayedMode,
    'net_profit',
    Boolean(snapshot.data) && !maintenance,
  );
  const refetchHome = homeQuery.refetch;
  const reconcile = useCallback(() => {
    void refetchHome();
  }, [refetchHome]);
  const streamEnabled = Boolean(snapshot.data && (!maintenance || session || showPendingResult));
  useEffect(() => {
    if (!streamEnabled || typeof EventSource === 'undefined') return undefined;
    return connectRPSStream({
      onConnection: setStream,
      onResync: (reason) => {
        setStream(reason === 'gap' || reason === 'malformed' ? 'gap' : 'disconnected');
        reconcile();
      },
      onReplace: (next) => {
        if (next.kind === 'queue' && next.queue.id === cancelledQueue.current) return;
        if (
          acked.current &&
          ((next.kind === 'session' && next.session.sessionID === acked.current) ||
            (next.kind === 'pending_result' && next.result.sessionID === acked.current))
        )
          return;
        const current = queryClient.getQueryData<RPSHomeState>(rpsKeys.state);
        if (shouldApplyRPSReplacement(current, next)) queryClient.setQueryData(rpsKeys.state, next);
      },
    });
  }, [queryClient, reconcile, streamEnabled]);
  useEffect(() => {
    if (!leaseSessionID) return undefined;
    const leaseID = createOpaqueID('gle_');
    let disposed = false;
    const renew = async () => {
      setLeaseState((current) => ({
        sessionID: leaseSessionID,
        value:
          current?.sessionID === leaseSessionID && current.value === 'active'
            ? 'active'
            : 'renewing',
      }));
      try {
        await renewRPSLease(leaseSessionID, leaseID);
        if (!disposed) setLeaseState({ sessionID: leaseSessionID, value: 'active' });
      } catch (error) {
        if (!disposed)
          setLeaseState({
            sessionID: leaseSessionID,
            value: isServiceStopped(error) ? 'stopped' : 'lost',
          });
        if (isConflict(error)) reconcile();
      }
    };
    void renew();
    const timer = window.setInterval(() => void renew(), 5_000);
    return () => {
      disposed = true;
      window.clearInterval(timer);
    };
  }, [leaseSessionID, reconcile]);
  const refreshAll = useCallback(async () => {
    await refetchHome();
    await queryClient.invalidateQueries({ queryKey: gameKeys.snapshot });
  }, [queryClient, refetchHome]);
  const execute = useCallback(
    async (intent: Operation) => {
      setOperation(intent);
      setOperationState('sending');
      setOperationError(null);
      try {
        if (intent.kind === 'queue') {
          await enqueueRPS(intent.mode, intent.token, intent.confirmed, intent.key);
          await refetchHome();
        } else if (intent.kind === 'cancel') {
          await cancelRPSQueue(intent.queueID, intent.revision, intent.key);
          cancelledQueue.current = intent.queueID;
          await refreshAll();
        } else {
          const next = await sendRPSAction(intent.intent);
          const current = queryClient.getQueryData<RPSHomeState>(rpsKeys.state);
          if (shouldApplyRPSReplacement(current, next))
            queryClient.setQueryData(rpsKeys.state, next);
          else await refetchHome();
        }
        setOperation(null);
        setOperationState('idle');
        setDeathmatchReview(false);
      } catch (error) {
        setOperationError(error);
        if (isResponseUnknown(error)) setOperationState('unknown');
        else {
          setOperation(null);
          setOperationState('idle');
          if (isConflict(error)) await refetchHome();
        }
      }
    },
    [queryClient, refetchHome, refreshAll],
  );
  const action = (value: RPSActionPayload) => {
    if (!session || operationState !== 'idle') return;
    void execute({
      kind: 'action',
      intent: {
        sessionID: session.sessionID,
        phaseSeq: session.phaseSeq,
        expectedRevision: session.revision,
        identityEpoch: session.identityEpoch,
        idempotencyKey: createIdempotencyKey(),
        value,
      },
    });
  };
  const sendACK = useCallback(async () => {
    if (home?.kind !== 'pending_result') return;
    const sessionID = home.result.sessionID;
    setAckStatus({ sessionID, value: 'sending' });
    try {
      await acknowledgeRPSResult(sessionID);
      setAckStatus(null);
      await refreshAll();
    } catch {
      setAckStatus({ sessionID, value: 'failed' });
      await refetchHome();
    }
  }, [home, refetchHome, refreshAll]);
  useEffect(() => {
    if (
      home?.kind !== 'pending_result' ||
      !showPendingResult ||
      acked.current === home.result.sessionID ||
      resultRef.current?.querySelectorAll('[data-result-seat]').length !== 3
    )
      return;
    acked.current = home.result.sessionID;
    let second = 0;
    const first = requestAnimationFrame(() => {
      second = requestAnimationFrame(() => void sendACK());
    });
    return () => {
      cancelAnimationFrame(first);
      if (second) cancelAnimationFrame(second);
    };
  }, [home, sendACK, showPendingResult]);
  const finishTutorial = async () => {
    setTutorialVisibility('closed');
    try {
      await markRPSTutorialSeen();
      setTutorialSaveFailed(false);
      await queryClient.invalidateQueries({ queryKey: gameKeys.snapshot });
    } catch {
      setTutorialSaveFailed(true);
    }
  };
  const queue = home?.kind === 'queue' ? home.queue : null;
  const queueRemaining = useAuthoritativeCountdown(
    queue ? `${queue.id}:${queue.revision}` : 'none',
    queue?.deadline ?? null,
    queue?.serverNow ?? 0,
    reconcile,
  );
  const modes = home?.kind === 'idle' ? home.modes : snapshot.data?.rps.modes;
  const selectedConfig = modes?.[displayedMode];
  const gateOpen = Boolean(
    snapshot.data?.gamesEnabled && snapshot.data.rps.enabled && selectedConfig?.enabled,
  );
  const minimumRequired = selectedConfig
    ? displayedMode === 'standard'
      ? multiplyCredits(selectedConfig.base, 5)
      : selectedConfig.base
    : null;
  const affordable = Boolean(
    snapshot.data &&
    minimumRequired &&
    creditsToMilli(snapshot.data.balance, true) >= creditsToMilli(minimumRequired),
  );
  const canQueue = gateOpen && affordable && homeQuery.isSuccess && !homeQuery.error;
  const deathmatchConfig = modes?.deathmatch;
  const deathmatchCommitment =
    deathmatchConfig && snapshot.data
      ? queueCommitment('deathmatch', deathmatchConfig.base, snapshot.data.balance)
      : null;
  const deathmatchGateOpen = Boolean(
    snapshot.data?.gamesEnabled && snapshot.data.rps.enabled && deathmatchConfig?.enabled,
  );
  const deathmatchAffordable = Boolean(
    snapshot.data &&
    deathmatchConfig &&
    creditsToMilli(snapshot.data.balance, true) >= creditsToMilli(deathmatchConfig.base),
  );
  const canQueueDeathmatch =
    deathmatchGateOpen && deathmatchAffordable && homeQuery.isSuccess && !homeQuery.error;
  const closeRules = useCallback(() => setRulesOpen(false), []);
  const rulesButton = (
    <GameRulesButton label={text('common.rulesButton')} onClick={() => setRulesOpen(true)} />
  );
  const rulesDialog = <RPSRules open={rulesOpen} onClose={closeRules} />;
  if (snapshot.isPending)
    return (
      <main className="game-page rps-page">
        <PageHeader
          back={
            <Link className="game-back-link" to="/games">
              {text('common.back')}
            </Link>
          }
          eyebrow={text('rps.eyebrow')}
          title={text('rps.title')}
          description={text('rps.description')}
          actions={rulesButton}
        />
        <LoadingState label={text('common.loading')} />
        {rulesDialog}
      </main>
    );
  if (snapshot.error && !maintenance)
    return (
      <main className="game-page rps-page">
        <PageHeader
          back={
            <Link className="game-back-link" to="/games">
              {text('common.back')}
            </Link>
          }
          eyebrow={text('rps.eyebrow')}
          title={text('rps.title')}
          description={text('rps.description')}
          actions={rulesButton}
        />
        <ErrorState error={snapshot.error} onRetry={() => void snapshot.refetch()} />
        {rulesDialog}
      </main>
    );
  if ((!snapshot.data || maintenance) && !session && !showPendingResult)
    return (
      <main className="game-page rps-page">
        <PageHeader
          back={
            <Link className="game-back-link" to="/games">
              {text('common.back')}
            </Link>
          }
          eyebrow={text('rps.eyebrow')}
          title={text('rps.title')}
          description={text('rps.description')}
          actions={rulesButton}
        />
        <p className="game-inline-notice game-inline-notice--warning">
          {text('common.maintenance')}
        </p>
        {rulesDialog}
      </main>
    );
  return (
    <main className="game-page rps-page">
      <PageHeader
        back={
          <Link className="game-back-link" to="/games">
            {text('common.back')}
          </Link>
        }
        eyebrow={text('rps.eyebrow')}
        title={text('rps.title')}
        description={text('rps.description')}
        actions={
          <>
            {rulesButton}
            <button
              type="button"
              className="btn btn-secondary"
              onClick={() => {
                setTutorialPage(0);
                setTutorialVisibility('open');
              }}
            >
              {text('rps.tutorial.replay')}
            </button>
          </>
        }
      />
      {maintenance ? (
        <p className="game-inline-notice game-inline-notice--warning">
          {text('common.maintenanceContinuation')}
        </p>
      ) : null}
      {tutorialSaveFailed ? (
        <p className="game-inline-notice game-inline-notice--warning">
          {text('rps.tutorial.saveFailed')}
        </p>
      ) : null}
      {homeQuery.isPending ? <LoadingState label={text('common.loading')} /> : null}
      {homeQuery.error ? (
        isServiceStopped(homeQuery.error) ? (
          <p className="game-inline-notice game-inline-notice--warning">
            {text('common.serviceStopped')}
          </p>
        ) : (
          <ErrorState error={homeQuery.error} onRetry={() => void homeQuery.refetch()} />
        )
      ) : null}
      {home?.kind === 'pending_result' && showPendingResult ? (
        <PendingResult
          result={home.result}
          ack={ack}
          onACK={() => {
            acked.current = home.result.sessionID;
            void sendACK();
          }}
          resultRef={resultRef}
        />
      ) : null}
      {queue ? (
        <Card className="rps-queue">
          <h2>{text('rps.pendingQueue')}</h2>
          <p>{text('rps.pendingQueuePrivacy')}</p>
          <StatusBadge
            active={stream === 'connected'}
            danger={stream === 'disconnected'}
            label={text(`rps.stream.${stream}` as 'rps.stream.connected')}
          />
          <p>
            {text(`rps.mode.${queue.mode}`)} · {queueRemaining ?? 0}s
          </p>
          <button
            type="button"
            className="btn btn-secondary"
            disabled={operationState !== 'idle'}
            onClick={() =>
              void execute({
                kind: 'cancel',
                queueID: queue.id,
                revision: queue.revision,
                key: createIdempotencyKey(),
              })
            }
          >
            {text('rps.cancelQueue')}
          </button>
        </Card>
      ) : null}
      {session ? (
        <Match
          state={session}
          lease={lease}
          stream={stream}
          busy={operationState !== 'idle' || lease !== 'active'}
          onAction={action}
          onRefresh={reconcile}
        />
      ) : null}
      {home?.kind === 'idle' ? (
        <Card className="rps-lobby">
          <h2>{text('center.rps.title')}</h2>
          <div className="rps-modes">
            {RPS_MODES.map((mode) => {
              const config = modes?.[mode];
              const commitment =
                config && snapshot.data
                  ? queueCommitment(mode, config.base, snapshot.data.balance)
                  : null;
              const open = Boolean(
                snapshot.data?.gamesEnabled && snapshot.data.rps.enabled && config?.enabled,
              );
              return (
                <button
                  type="button"
                  key={mode}
                  className={displayedMode === mode ? 'is-selected' : ''}
                  aria-pressed={displayedMode === mode}
                  onClick={() => setSelectedMode(mode)}
                >
                  <strong>{text(`rps.mode.${mode}`)}</strong>
                  <span>{text(`rps.mode.${mode}Help`)}</span>
                  {config ? (
                    <>
                      <span>
                        {text('rps.base')}: <Money value={config.base} />
                      </span>
                      <span>
                        {text('rps.entry')}: <Money value={commitment!} />
                      </span>
                      <span>{text('rps.queueTime', { seconds: config.queueSeconds })}</span>
                    </>
                  ) : null}
                  <StatusBadge
                    active={open}
                    danger={!open}
                    label={text(open ? 'common.open' : 'common.closed')}
                  />
                </button>
              );
            })}
          </div>
          {gateOpen && !affordable ? (
            <p className="game-inline-notice game-inline-notice--danger">
              {text('rps.insufficient')}
            </p>
          ) : null}
          <button
            type="button"
            className="btn btn-primary"
            disabled={!canQueue || operationState !== 'idle'}
            onClick={() => {
              if (displayedMode === 'deathmatch') setDeathmatchReview(true);
              else
                void execute({
                  kind: 'queue',
                  mode: displayedMode,
                  token: rpsDeviceToken(),
                  confirmed: false,
                  key: createIdempotencyKey(),
                });
            }}
          >
            {text('rps.queue', { mode: text(`rps.mode.${displayedMode}`) })}
          </button>
        </Card>
      ) : null}
      {snapshot.data && home?.kind !== 'pending_result' ? (
        <section className="rps-leaderboards">
          <div className="rps-board-heading">
            <h2>{text('rps.leaderboard.title')}</h2>
            <span>{text(`rps.mode.${displayedMode}`)}</span>
          </div>
          {profit.isPending || net.isPending ? (
            <LoadingState label={text('common.loading')} />
          ) : null}
          {profit.error ? (
            <ErrorState error={profit.error} onRetry={() => void profit.refetch()} />
          ) : null}
          {net.error ? <ErrorState error={net.error} onRetry={() => void net.refetch()} /> : null}
          {(!profit.isPending && !profit.error) || (!net.isPending && !net.error) ? (
            <div className="rps-board-grid">
              {!profit.isPending && !profit.error ? (
                <Leaderboard data={profit.data} board="profit_rate" />
              ) : null}
              {!net.isPending && !net.error ? (
                <Leaderboard data={net.data} board="net_profit" />
              ) : null}
            </div>
          ) : null}
        </section>
      ) : null}
      {deathmatchReview ? (
        <div
          className="rps-modal"
          role="dialog"
          aria-modal="true"
          aria-labelledby="rps-deathmatch-title"
        >
          <Card>
            <h2 id="rps-deathmatch-title">{text('rps.mode.deathmatch')}</h2>
            <p>{text('rps.deathmatchReview')}</p>
            {deathmatchCommitment ? (
              <p>
                <strong>{text('rps.entry')}:</strong> <Money value={deathmatchCommitment} />
              </p>
            ) : null}
            <div className="game-state-actions">
              <button
                type="button"
                className="btn btn-secondary"
                onClick={() => setDeathmatchReview(false)}
              >
                {text('linklink.keep')}
              </button>
              <button
                type="button"
                className="btn btn-danger"
                disabled={!canQueueDeathmatch || operationState !== 'idle'}
                onClick={() =>
                  void execute({
                    kind: 'queue',
                    mode: 'deathmatch',
                    token: rpsDeviceToken(),
                    confirmed: true,
                    key: createIdempotencyKey(),
                  })
                }
              >
                {text('rps.deathmatchConfirm')}
              </button>
            </div>
          </Card>
        </div>
      ) : null}
      {tutorialOpen ? (
        <Tutorial
          page={tutorialPage}
          onPage={setTutorialPage}
          onSkip={() => {
            setTutorialVisibility('closed');
          }}
          onFinish={() => void finishTutorial()}
        />
      ) : null}
      {operationState === 'unknown' ? (
        <div className="game-inline-notice game-inline-notice--warning" role="alert">
          <p>{text('common.responseUnknown')}</p>
          <div className="game-state-actions">
            <button type="button" className="btn btn-secondary" onClick={reconcile}>
              {text('common.retry')}
            </button>
            {operation ? (
              <button
                type="button"
                className="btn btn-primary"
                onClick={() => void execute(operation)}
              >
                {text('fishing.unknown.replay')}
              </button>
            ) : null}
          </div>
        </div>
      ) : null}
      {operationError && operationState !== 'unknown' && !isConflict(operationError) ? (
        <ErrorState error={operationError} onRetry={reconcile} />
      ) : null}
      <p className="game-footnote">{text('common.noHistory')}</p>
      {rulesDialog}
    </main>
  );
}
