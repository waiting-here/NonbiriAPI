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
import { creditsToMilli, formatCredits } from '../common/strict';
import { useAuthoritativeCountdown } from '../common/countdown';
import { LINKLINK_SPECS, type LinkLinkSpec } from '../common/types';
import { gameKeys, useGamesSnapshot } from '../common/snapshot';
import {
  abandonLinkLink,
  linkLinkKeys,
  matchLinkLink,
  renewLinkLinkLease,
  startLinkLink,
  useLinkLinkCurrent,
} from './api';
import { boardWasRearranged, shouldApplyLinkLinkReplacement } from './normalize';
import { TileGlyph } from './TileGlyph';
import type {
  LinkLinkCoordinate,
  LinkLinkCurrent,
  LinkLinkMatchIntent,
  LinkLinkState,
  LinkLinkSummary,
} from './types';
import './linklink.css';

type MutationIntent =
  | { readonly kind: 'start'; readonly spec: LinkLinkSpec; readonly key: string }
  | { readonly kind: 'match'; readonly value: LinkLinkMatchIntent }
  | {
      readonly kind: 'abandon';
      readonly sessionID: string;
      readonly revision: string;
      readonly key: string;
    };
type LinkFeedback = 'accepted' | 'rejected' | 'rearranged';

function isActiveLinkLink(value: LinkLinkCurrent | undefined): value is LinkLinkState {
  return value?.kind === 'active';
}

function isLinkLinkSummary(value: LinkLinkCurrent | undefined): value is LinkLinkSummary {
  return value?.kind === 'summary';
}

function Money({ value }: { readonly value: string }) {
  const { text } = useGameCopy();
  return (
    <span className="game-money">
      {formatCredits(value)} <span className="game-money__unit">{text('common.credits')}</span>
    </span>
  );
}

function LinkLinkRules({
  open,
  onClose,
}: {
  readonly open: boolean;
  readonly onClose: () => void;
}) {
  const { text } = useGameCopy();
  const sections: readonly GameRulesSection[] = [
    {
      title: text('linklink.rules.goalTitle'),
      paragraphs: [text('linklink.rules.goalBody')],
    },
    {
      title: text('linklink.rules.boardTitle'),
      paragraphs: [text('linklink.rules.boardBody')],
    },
    {
      title: text('linklink.rules.pathTitle'),
      paragraphs: [text('linklink.rules.pathBody')],
    },
    {
      title: text('linklink.rules.scoreTitle'),
      paragraphs: [text('linklink.rules.scoreBody')],
    },
    {
      title: text('linklink.rules.shuffleTitle'),
      paragraphs: [text('linklink.rules.shuffleBody')],
    },
    {
      title: text('linklink.rules.clockTitle'),
      paragraphs: [text('linklink.rules.clockBody')],
    },
    {
      title: text('linklink.rules.recoveryTitle'),
      paragraphs: [text('linklink.rules.recoveryBody')],
    },
    {
      title: text('linklink.rules.abandonTitle'),
      paragraphs: [text('linklink.rules.abandonBody')],
    },
  ];
  return (
    <GameRulesDialog
      open={open}
      title={text('linklink.rules.title')}
      closeLabel={text('common.closeRules')}
      sections={sections}
      onClose={onClose}
    />
  );
}

function LinkBoard({
  state,
  selected,
  busy,
  onSelect,
}: {
  readonly state: LinkLinkState;
  readonly selected: LinkLinkCoordinate | null;
  readonly busy: boolean;
  readonly onSelect: (coordinate: LinkLinkCoordinate) => void;
}) {
  const { text } = useGameCopy();
  const active = state.board.tiles.filter((tile) => !tile.removed);
  const [focus, setFocus] = useState(() => (active[0] ? `${active[0].row}:${active[0].col}` : ''));
  const refs = useRef(new Map<string, HTMLButtonElement>());
  const effectiveFocus = active.some((tile) => `${tile.row}:${tile.col}` === focus)
    ? focus
    : active[0]
      ? `${active[0].row}:${active[0].col}`
      : '';
  const focusAt = (row: number, col: number) => {
    const candidates = active;
    const exact = candidates.find((tile) => tile.row === row && tile.col === col);
    const fallback = candidates.reduce<(typeof candidates)[number] | undefined>((best, tile) => {
      const distance = Math.abs(tile.row - row) + Math.abs(tile.col - col);
      if (!best) return tile;
      const bestDistance = Math.abs(best.row - row) + Math.abs(best.col - col);
      return distance < bestDistance ? tile : best;
    }, undefined);
    const target = exact ?? fallback;
    if (target) {
      const key = `${target.row}:${target.col}`;
      setFocus(key);
      refs.current.get(key)?.focus();
    }
  };
  return (
    <div className={`linklink-board-scroll linklink-board-scroll--${state.spec}`}>
      <div
        className="linklink-board"
        style={{ '--linklink-cols': state.board.cols } as React.CSSProperties}
        role="grid"
        aria-rowcount={state.board.rows}
        aria-colcount={state.board.cols}
        aria-label={text('linklink.active')}
      >
        {state.board.tiles.map((tile) => {
          const key = `${tile.row}:${tile.col}`;
          const isSelected = selected?.row === tile.row && selected.col === tile.col;
          return (
            <button
              type="button"
              role="gridcell"
              aria-rowindex={tile.row + 1}
              aria-colindex={tile.col + 1}
              aria-selected={isSelected}
              aria-label={`${tile.tileKey}, ${tile.row + 1}, ${tile.col + 1}`}
              className={`linklink-tile${isSelected ? ' is-selected' : ''}${tile.removed ? ' is-removed' : ''}`}
              disabled={tile.removed || busy}
              tabIndex={!tile.removed && key === effectiveFocus ? 0 : -1}
              key={key}
              ref={(node) => {
                if (node) refs.current.set(key, node);
                else refs.current.delete(key);
              }}
              onFocus={() => setFocus(key)}
              onClick={() => onSelect({ row: tile.row, col: tile.col })}
              onKeyDown={(event) => {
                const delta =
                  event.key === 'ArrowLeft'
                    ? [0, -1]
                    : event.key === 'ArrowRight'
                      ? [0, 1]
                      : event.key === 'ArrowUp'
                        ? [-1, 0]
                        : event.key === 'ArrowDown'
                          ? [1, 0]
                          : null;
                if (!delta) return;
                event.preventDefault();
                focusAt(tile.row + delta[0], tile.col + delta[1]);
              }}
            >
              {tile.removed ? <span aria-hidden="true" /> : <TileGlyph tileKey={tile.tileKey} />}
            </button>
          );
        })}
      </div>
    </div>
  );
}

function SummaryCard({
  summary,
  onNew,
}: {
  readonly summary: LinkLinkSummary;
  readonly onNew: () => void;
}) {
  const { text } = useGameCopy();
  const elapsed = Math.max(0, Math.min(summary.terminalAt, summary.deadline) - summary.startedAt);
  const remaining = Math.max(0, summary.deadline - summary.terminalAt);
  return (
    <Card className="linklink-summary">
      <p className="eyebrow">{text(`linklink.summary.${summary.terminalReason}`)}</p>
      <h2>{text(`linklink.spec`, { spec: summary.spec })}</h2>
      <dl className="linklink-facts">
        <div>
          <dt>
            {text('linklink.progress', {
              removed: summary.pairsRemoved,
              total: summary.totalPairs,
            })}
          </dt>
          <dd>
            {summary.pairsRemoved}/{summary.totalPairs}
          </dd>
        </div>
        <div>
          <dt>{text('linklink.summary.score')}</dt>
          <dd>
            {summary.score === null ? text('linklink.summary.none') : formatCredits(summary.score)}
          </dd>
        </div>
        <div>
          <dt>{text('linklink.summary.elapsed')}</dt>
          <dd>{text('linklink.summary.seconds', { seconds: elapsed })}</dd>
        </div>
        <div>
          <dt>{text('linklink.summary.remaining')}</dt>
          <dd>{text('linklink.summary.seconds', { seconds: remaining })}</dd>
        </div>
      </dl>
      <button type="button" className="btn btn-primary" onClick={onNew}>
        {text('linklink.summary.new')}
      </button>
    </Card>
  );
}

export function LinkLinkGame() {
  const { text } = useGameCopy();
  const queryClient = useQueryClient();
  const snapshot = useGamesSnapshot();
  const maintenance = isMaintenance(snapshot.error);
  const current = useLinkLinkCurrent(Boolean(snapshot.data));
  const [selectedSpec, setSelectedSpec] = useState<LinkLinkSpec>('6x8');
  const [rulesOpen, setRulesOpen] = useState(false);
  const [review, setReview] = useState(false);
  const [abandonReview, setAbandonReview] = useState(false);
  const [selection, setSelection] = useState<{
    readonly identity: string;
    readonly coordinate: LinkLinkCoordinate;
  } | null>(null);
  const [mutation, setMutation] = useState<MutationIntent | null>(null);
  const [mutationState, setMutationState] = useState<'idle' | 'sending' | 'unknown'>('idle');
  const [mutationError, setMutationError] = useState<unknown>(null);
  const [feedbackState, setFeedbackState] = useState<{
    readonly identity: string;
    readonly value: LinkFeedback;
  } | null>(null);
  const [leaseState, setLeaseState] = useState<{
    readonly sessionID: string;
    readonly value: 'renewing' | 'active' | 'lost' | 'stopped';
  } | null>(null);
  const state: LinkLinkState | null = isActiveLinkLink(current.data) ? current.data : null;
  const summary: LinkLinkSummary | null = isLinkLinkSummary(current.data) ? current.data : null;
  const refetchCurrent = current.refetch;
  const stateIdentity = state ? `${state.sessionID}:${state.revision}` : null;
  const selected = selection?.identity === stateIdentity ? selection.coordinate : null;
  const feedback = feedbackState?.identity === stateIdentity ? feedbackState.value : null;
  const sessionID = state?.sessionID ?? null;
  const lease = sessionID
    ? leaseState?.sessionID === sessionID
      ? leaseState.value
      : 'renewing'
    : 'none';

  const reconcile = useCallback(async () => {
    await refetchCurrent();
    await queryClient.invalidateQueries({ queryKey: gameKeys.snapshot });
  }, [queryClient, refetchCurrent]);
  const reconcileElapsed = useCallback(() => void reconcile(), [reconcile]);
  const remaining = useAuthoritativeCountdown(
    stateIdentity ?? 'none',
    state?.deadline ?? null,
    state?.serverNow ?? 0,
    reconcileElapsed,
  );

  useEffect(() => {
    if (!sessionID) return undefined;
    const leaseID = createOpaqueID('gle_');
    let disposed = false;
    const renew = async () => {
      setLeaseState((current) => ({
        sessionID,
        value:
          current?.sessionID === sessionID && current.value === 'active' ? 'active' : 'renewing',
      }));
      try {
        await renewLinkLinkLease(sessionID, leaseID);
        if (!disposed) setLeaseState({ sessionID, value: 'active' });
      } catch (error) {
        if (!disposed)
          setLeaseState({
            sessionID,
            value: isServiceStopped(error) ? 'stopped' : 'lost',
          });
        if (isConflict(error)) void refetchCurrent();
      }
    };
    void renew();
    const timer = window.setInterval(() => void renew(), 5_000);
    return () => {
      disposed = true;
      window.clearInterval(timer);
    };
  }, [refetchCurrent, sessionID]);

  const adopt = useCallback(
    (value: LinkLinkState | LinkLinkSummary) => {
      let applied = false;
      queryClient.setQueryData<LinkLinkCurrent>(linkLinkKeys.current, (cached) => {
        if (!shouldApplyLinkLinkReplacement(cached, value)) return cached;
        applied = true;
        return value;
      });
      return applied;
    },
    [queryClient],
  );

  const execute = useCallback(
    async (intent: MutationIntent) => {
      setMutation(intent);
      setMutationState('sending');
      setMutationError(null);
      setFeedbackState(null);
      try {
        if (intent.kind === 'start') {
          if (!adopt(await startLinkLink(intent.spec, intent.key))) await refetchCurrent();
          setReview(false);
        } else if (intent.kind === 'match') {
          const before = isActiveLinkLink(current.data) ? current.data : null;
          const next = await matchLinkLink(intent.value);
          const applied = adopt(next);
          if (!applied) await refetchCurrent();
          setFeedbackState(
            applied && isActiveLinkLink(next)
              ? {
                  identity: `${next.sessionID}:${next.revision}`,
                  value: before && boardWasRearranged(before, next) ? 'rearranged' : 'accepted',
                }
              : null,
          );
        } else {
          if (!adopt(await abandonLinkLink(intent.sessionID, intent.revision, intent.key)))
            await refetchCurrent();
          setAbandonReview(false);
        }
        setMutation(null);
        setMutationState('idle');
        setSelection(null);
        await queryClient.invalidateQueries({ queryKey: gameKeys.snapshot });
      } catch (error) {
        setMutationError(error);
        if (isResponseUnknown(error)) setMutationState('unknown');
        else {
          setMutation(null);
          setMutationState('idle');
          if (intent.kind === 'match')
            setFeedbackState({
              identity: `${intent.value.sessionID}:${intent.value.expectedRevision}`,
              value: 'rejected',
            });
          if (isConflict(error)) await reconcile();
        }
      }
    },
    [adopt, current.data, queryClient, reconcile, refetchCurrent],
  );

  const chooseTile = (coordinate: LinkLinkCoordinate) => {
    if (!state || mutationState !== 'idle') return;
    if (!selected) {
      setSelection({ identity: `${state.sessionID}:${state.revision}`, coordinate });
      return;
    }
    if (selected.row === coordinate.row && selected.col === coordinate.col) {
      setSelection(null);
      return;
    }
    void execute({
      kind: 'match',
      value: {
        sessionID: state.sessionID,
        expectedRevision: state.revision,
        first: selected,
        second: coordinate,
        idempotencyKey: createIdempotencyKey(),
      },
    });
  };

  const spec = snapshot.data?.linklink.specs[selectedSpec];
  const gateOpen = Boolean(
    snapshot.data?.gamesEnabled && snapshot.data.linklink.enabled && spec?.enabled,
  );
  const affordable = Boolean(
    snapshot.data &&
    spec &&
    creditsToMilli(snapshot.data.balance, true) >= creditsToMilli(spec.price),
  );
  const canStart = current.isSuccess && gateOpen && affordable;
  const closeRules = useCallback(() => setRulesOpen(false), []);
  const rulesButton = (
    <GameRulesButton label={text('common.rulesButton')} onClick={() => setRulesOpen(true)} />
  );
  const rulesDialog = <LinkLinkRules open={rulesOpen} onClose={closeRules} />;

  if (snapshot.isPending)
    return (
      <main className="game-page linklink-page">
        <PageHeader
          back={
            <Link className="game-back-link" to="/games">
              {text('common.back')}
            </Link>
          }
          eyebrow={text('linklink.eyebrow')}
          title={text('linklink.title')}
          description={text('linklink.description')}
          actions={rulesButton}
        />
        <LoadingState label={text('common.loading')} />
        {rulesDialog}
      </main>
    );
  if (snapshot.error && !maintenance)
    return (
      <main className="game-page linklink-page">
        <PageHeader
          back={
            <Link className="game-back-link" to="/games">
              {text('common.back')}
            </Link>
          }
          eyebrow={text('linklink.eyebrow')}
          title={text('linklink.title')}
          description={text('linklink.description')}
          actions={rulesButton}
        />
        <ErrorState error={snapshot.error} onRetry={() => void snapshot.refetch()} />
        {rulesDialog}
      </main>
    );
  if ((!snapshot.data || maintenance) && !state)
    return (
      <main className="game-page linklink-page">
        <PageHeader
          back={
            <Link className="game-back-link" to="/games">
              {text('common.back')}
            </Link>
          }
          eyebrow={text('linklink.eyebrow')}
          title={text('linklink.title')}
          description={text('linklink.description')}
          actions={rulesButton}
        />
        <p className="game-inline-notice game-inline-notice--warning">
          {text('common.maintenance')}
        </p>
        {rulesDialog}
      </main>
    );

  return (
    <main className="game-page linklink-page">
      <PageHeader
        back={
          <Link className="game-back-link" to="/games">
            {text('common.back')}
          </Link>
        }
        eyebrow={text('linklink.eyebrow')}
        title={text('linklink.title')}
        description={text('linklink.description')}
        actions={rulesButton}
      />
      {maintenance ? (
        <p className="game-inline-notice game-inline-notice--warning">
          {text('common.maintenanceContinuation')}
        </p>
      ) : null}
      {current.isPending ? <LoadingState label={text('common.loading')} /> : null}
      {current.error && !state ? (
        <ErrorState error={current.error} onRetry={() => void current.refetch()} />
      ) : null}
      {state ? (
        <div className="linklink-active">
          <Card className="linklink-status">
            <div className="linklink-heading">
              <div>
                <p className="eyebrow">{text('linklink.active')}</p>
                <h2>{text('linklink.spec', { spec: state.spec })}</h2>
              </div>
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
            <div className="linklink-progress">
              <progress max={state.totalPairs} value={state.pairsRemoved} />
              <span>
                {text('linklink.progress', {
                  removed: state.pairsRemoved,
                  total: state.totalPairs,
                })}
              </span>
            </div>
            <p>
              <strong>{text('linklink.deadline')}:</strong>{' '}
              {new Date(state.deadline * 1000).toLocaleTimeString()} ·{' '}
              {text('linklink.remaining', { seconds: remaining ?? 0 })}
            </p>
            <p role="status">
              {mutationState === 'sending'
                ? text('linklink.matching')
                : selected
                  ? text('linklink.selectSecond')
                  : text('linklink.selectFirst')}
            </p>
            {feedback ? (
              <p
                className={`game-inline-notice${feedback === 'rejected' ? ' game-inline-notice--warning' : ''}`}
              >
                {text(
                  feedback === 'accepted'
                    ? 'linklink.matchAccepted'
                    : feedback === 'rearranged'
                      ? 'linklink.rearranged'
                      : 'linklink.matchRejected',
                )}
              </p>
            ) : null}
          </Card>
          <LinkBoard
            state={state}
            selected={selected}
            busy={mutationState !== 'idle' || lease !== 'active'}
            onSelect={chooseTile}
          />
          <button
            type="button"
            className="btn btn-secondary linklink-abandon"
            disabled={mutationState !== 'idle' || lease !== 'active'}
            onClick={() => setAbandonReview(true)}
          >
            {text('linklink.abandon')}
          </button>
        </div>
      ) : summary ? (
        <SummaryCard
          summary={summary}
          onNew={() => {
            queryClient.setQueryData(linkLinkKeys.current, null);
            setSelectedSpec(summary.spec);
          }}
        />
      ) : null}
      {!state ? (
        <Card className="linklink-lobby">
          <h2>{text('linklink.startReview')}</h2>
          {current.isSuccess && current.data === null ? <p>{text('linklink.empty')}</p> : null}
          <div className="linklink-specs">
            {LINKLINK_SPECS.map((value) => {
              const valueSpec = snapshot.data?.linklink.specs[value];
              const open = Boolean(
                snapshot.data?.gamesEnabled && snapshot.data.linklink.enabled && valueSpec?.enabled,
              );
              return (
                <button
                  type="button"
                  key={value}
                  className={selectedSpec === value ? 'is-selected' : ''}
                  aria-pressed={selectedSpec === value}
                  onClick={() => setSelectedSpec(value)}
                >
                  <strong>{text('linklink.spec', { spec: value })}</strong>
                  <span>{valueSpec ? <Money value={valueSpec.price} /> : '—'}</span>
                  <span>
                    {valueSpec ? text('linklink.seconds', { seconds: valueSpec.seconds }) : '—'}
                  </span>
                  <StatusBadge
                    active={open}
                    danger={!open}
                    label={open ? text('common.open') : text('linklink.specClosed')}
                  />
                </button>
              );
            })}
          </div>
          {!gateOpen ? (
            <p className="game-inline-notice game-inline-notice--warning">
              {text('linklink.specClosed')}
            </p>
          ) : null}
          {gateOpen && !affordable ? (
            <p className="game-inline-notice game-inline-notice--danger">
              {text('fishing.insufficient')}
            </p>
          ) : null}
          <button
            type="button"
            className="btn btn-primary"
            disabled={!canStart || mutationState !== 'idle'}
            onClick={() => setReview(true)}
          >
            {text('linklink.start', { spec: selectedSpec })}
          </button>
        </Card>
      ) : null}
      {review ? (
        <div
          className="linklink-modal"
          role="dialog"
          aria-modal="true"
          aria-labelledby="linklink-start-title"
        >
          <Card>
            <h2 id="linklink-start-title">{text('linklink.startReview')}</h2>
            <p>{text('linklink.startConsequences')}</p>
            <p>
              <strong>{text('linklink.spec', { spec: selectedSpec })}</strong> ·{' '}
              {spec ? <Money value={spec.price} /> : null}
            </p>
            <div className="game-state-actions">
              <button type="button" className="btn btn-secondary" onClick={() => setReview(false)}>
                {text('linklink.keep')}
              </button>
              <button
                type="button"
                className="btn btn-primary"
                disabled={!canStart || mutationState !== 'idle'}
                onClick={() =>
                  void execute({ kind: 'start', spec: selectedSpec, key: createIdempotencyKey() })
                }
              >
                {text('linklink.start', { spec: selectedSpec })}
              </button>
            </div>
          </Card>
        </div>
      ) : null}
      {abandonReview && state ? (
        <div
          className="linklink-modal"
          role="dialog"
          aria-modal="true"
          aria-labelledby="linklink-abandon-title"
        >
          <Card>
            <h2 id="linklink-abandon-title">{text('linklink.abandonTitle')}</h2>
            <p>{text('linklink.abandonBody')}</p>
            <div className="game-state-actions">
              <button
                type="button"
                className="btn btn-secondary"
                onClick={() => setAbandonReview(false)}
              >
                {text('linklink.keep')}
              </button>
              <button
                type="button"
                className="btn btn-danger"
                disabled={mutationState !== 'idle' || lease !== 'active'}
                onClick={() =>
                  void execute({
                    kind: 'abandon',
                    sessionID: state.sessionID,
                    revision: state.revision,
                    key: createIdempotencyKey(),
                  })
                }
              >
                {text('linklink.confirmAbandon')}
              </button>
            </div>
          </Card>
        </div>
      ) : null}
      {mutationState === 'unknown' ? (
        <div className="game-inline-notice game-inline-notice--warning" role="alert">
          <p>{text('common.responseUnknown')}</p>
          <div className="game-state-actions">
            <button type="button" className="btn btn-secondary" onClick={() => void reconcile()}>
              {text('common.retry')}
            </button>
            {mutation ? (
              <button
                type="button"
                className="btn btn-primary"
                onClick={() => void execute(mutation)}
              >
                {text('fishing.unknown.replay')}
              </button>
            ) : null}
          </div>
        </div>
      ) : null}
      {mutationError && mutationState !== 'unknown' && !isConflict(mutationError) ? (
        <ErrorState error={mutationError} onRetry={() => void reconcile()} />
      ) : null}
      {rulesDialog}
    </main>
  );
}
