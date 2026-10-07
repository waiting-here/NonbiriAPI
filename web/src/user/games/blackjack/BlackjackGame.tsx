import { GameHeaderTool } from '../common/GameHeader';
import { GameBackLink } from '../common/GameBackLink';
import { GameActionBar } from '../common/GameActionBar';
import { useCallback, useEffect, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  BLACKJACK_ACTIONS,
  BLACKJACK_EMOTES,
  type BlackjackAction,
  type BlackjackState,
} from '@shared/games/blackjack';
import { ErrorState, LoadingState } from '@shared/components/States';
import { GameWallets } from '../common/GameWallets';
import { Leaderboard } from '../ranking/Leaderboard';
import { LeaderboardTabs } from '../ranking/LeaderboardTabs';
import { OnboardingCard } from '../common/OnboardingCard';
import { RandomnessProof } from '../common/RandomnessProof';
import { useAuthoritativeCountdown } from '../common/countdown';
import { useDuelText } from '../common/duel/copy';
import { DuelDialog } from '../common/duel/Dialog';
import { creditsToMilli, formatCredits } from '../common/strict';
import { useGamesSnapshot, gameKeys } from '../common/snapshot';
import { blackjackKeys, readBlackjackDetail, readBlackjackHistory, useBlackjack } from './api';
import { BlackjackBoard, BlackjackSettlement, emoteText } from './Table';
import { blackjackAudioFacts } from './audioFacts';
import { useArcadeAudio } from '../common/audio/useArcadeAudio';
import { useSnapshotAudioFacts } from '../common/audio/useSnapshotAudioFacts';
import { ArcadeAudioControls } from '../common/audio/ArcadeAudioControls';
import '../games.css';
import '../common/duel/duel.css';
import '../bidding/bidding.css';
import './blackjack.css';
function useTableMotion(home: BlackjackState | undefined) {
  const surface = useRef<HTMLDivElement>(null);
  const previous = useRef<{
    time: number;
    cards: Set<string>;
    scores: Set<string>;
    result: string | null;
  } | null>(null);
  const client = useQueryClient();
  useEffect(() => {
    const node = surface.current;
    if (!home || !node) return;
    const cardNodes = [...node.querySelectorAll<HTMLElement>('[data-card-key]')];
    const scoreNodes = [...node.querySelectorAll<HTMLElement>('[data-score-key]')];
    const result = home.table?.terminal_at ? `${home.table.id}:${home.table.revision}` : null;
    const next = {
      time: home.server_now,
      cards: new Set(cardNodes.map((n) => n.dataset.cardKey ?? '')),
      scores: new Set(scoreNodes.map((n) => n.dataset.scoreKey ?? '')),
      result,
    };
    const before = previous.current;
    previous.current = next;
    if (!before || home.server_now - before.time > 3 || document.visibilityState !== 'visible')
      return;
    const reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
    let dealt = 0;
    for (const card of cardNodes)
      if (!before.cards.has(card.dataset.cardKey ?? '')) {
        if (!reduced)
          card.animate?.(
            [
              { opacity: 0, transform: 'translate(35px, -24px) rotateY(75deg) rotate(8deg)' },
              { opacity: 1, transform: 'translate(0, 0) rotateY(0) rotate(0)' },
            ],
            {
              duration: 460,
              delay: Math.min(dealt, 5) * 65,
              easing: 'cubic-bezier(.17,.8,.25,1)',
              fill: 'backwards',
            },
          );
        dealt++;
      }
    for (const score of scoreNodes)
      if (!reduced && !before.scores.has(score.dataset.scoreKey ?? ''))
        score.animate?.(
          [{ transform: 'scale(1.35)', color: '#c28b37' }, { transform: 'scale(1)' }],
          { duration: 550, easing: 'ease-out' },
        );
    if (result && result !== before.result) {
      if (!reduced)
        node.querySelector<HTMLElement>('[data-settlement]')?.animate?.(
          [
            { opacity: 0, transform: 'translateY(12px) scale(.96)' },
            { opacity: 1, transform: 'translateY(0) scale(1)' },
          ],
          { duration: 650, easing: 'ease-out' },
        );
      void client.invalidateQueries({ queryKey: gameKeys.snapshot });
      void client.invalidateQueries({ queryKey: [...blackjackKeys.root, 'history'] });
    }
  }, [home, client]);
  return surface;
}
function Rules({ close }: { readonly close: () => void }) {
  const text = useDuelText();
  return (
    <DuelDialog title={text('blackjack.blackjackTableRules')} onClose={close}>
      <h3>{text('blackjack.oneRoundEvery30SecondsUpTo')}</h3>
      <p>{text('blackjack.every30Seconds5SecondsForSeating')}</p>
      <p>{text('blackjack.joiningReservesYourStakeUsingGameCredits')}</p>
      <h3>{text('blackjack.sixDecksAndTheDealer')}</h3>
      <p>{text('blackjack.sixCompleteDecksAreShuffledAnewEach')}</p>
      <h3>{text('blackjack.hitStandDoubleAndSplit')}</h3>
      <p>{text('blackjack.splitEqualValueCardsOnceIntoTwo')}</p>
      <h3>{text('blackjack.returnsAndFees')}</h3>
      <p>{text('blackjack.grossReturnsAreZeroForALoss')}</p>
      <p>{text('blackjack.disconnectionDoesNotPauseTheGameA')}</p>
    </DuelDialog>
  );
}
function History({ close }: { readonly close: () => void }) {
  const text = useDuelText();
  const [cursors, setCursors] = useState<(string | null)[]>([null]);
  const [selected, setSelected] = useState<string | null>(null);
  const cursor = cursors.at(-1) ?? null;
  const page = useQuery({
    queryKey: [...blackjackKeys.root, 'history', cursor],
    queryFn: ({ signal }) => readBlackjackHistory(cursor, signal),
    retry: false,
  });
  const detail = useQuery({
    queryKey: [...blackjackKeys.root, 'history', 'detail', selected],
    queryFn: ({ signal }) => readBlackjackDetail(selected ?? '', signal),
    enabled: selected !== null,
    retry: false,
  });
  return (
    <DuelDialog title={text('blackjack.blackjackHistory')} onClose={close}>
      <p>{text('blackjack.yourSettledTablesFromTheLast30')}</p>
      {selected ? (
        <>
          <button className="btn btn-secondary" onClick={() => setSelected(null)}>
            {text('blackjack.backToList')}
          </button>
          {detail.isPending ? (
            <LoadingState />
          ) : detail.error ? (
            <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />
          ) : (
            <div className="bidding-game blackjack-game">
              <RandomnessProof game="blackjack" id={detail.data.table.id} terminal />
              <BlackjackBoard table={detail.data.table} ownSeat={detail.data.summary.seat} />
              {detail.data.table.phase === 'result' ? (
                <BlackjackSettlement
                  fact={detail.data.table.fact}
                  seat={detail.data.summary.seat}
                />
              ) : (
                <p>{text('blackjack.systemCancellationAllOriginalPaymentsRefunded')}</p>
              )}
            </div>
          )}
        </>
      ) : (
        <>
          {page.isPending ? (
            <LoadingState />
          ) : page.error ? (
            <ErrorState error={page.error} onRetry={() => void page.refetch()} />
          ) : (
            <>
              <div className="bj-history-list">
                {page.data.items.map((h) => (
                  <button
                    key={h.id}
                    className="btn btn-secondary"
                    onClick={() => setSelected(h.id)}
                  >
                    <span>{new Date(h.started_at * 1000).toLocaleString()}</span>
                    <span>
                      {h.phase === 'cancelled'
                        ? text('blackjack.refunded')
                        : `${text('blackjack.generalPaid')} ${formatCredits(h.net)}`}
                    </span>
                  </button>
                ))}
              </div>
              {!page.data.items.length && <p>{text('blackjack.noGamesYet')}</p>}
            </>
          )}
          <nav className="duel-actions" aria-label={text('blackjack.historyPages')}>
            <button
              className="btn btn-secondary"
              disabled={cursors.length === 1}
              onClick={() => setCursors((v) => v.slice(0, -1))}
            >
              {text('blackjack.previous')}
            </button>
            <button
              className="btn btn-secondary"
              disabled={!page.data?.next_cursor}
              onClick={() => {
                if (page.data?.next_cursor) setCursors((v) => [...v, page.data.next_cursor]);
              }}
            >
              {text('blackjack.next')}
            </button>
          </nav>
        </>
      )}
    </DuelDialog>
  );
}
function QueueForm({
  config,
  blocked,
  accepting,
  onQueue,
}: {
  readonly config: BlackjackState['config'];
  readonly blocked: boolean;
  readonly accepting: boolean;
  readonly onQueue: (stake: string) => void;
}) {
  const text = useDuelText();
  const [stake, setStake] = useState(config.default_stake);
  let selected: bigint | null = null;
  let valid = false;
  try {
    selected = creditsToMilli(stake);
    const min = creditsToMilli(config.min_stake),
      step = creditsToMilli(config.stake_step);
    valid =
      step > 0n &&
      selected >= min &&
      selected <= creditsToMilli(config.max_stake) &&
      (selected - min) % step === 0n;
  } catch {
    /* Invalid input remains editable. */
  }
  const current = config.quick_stakes !== undefined;
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        if (!blocked && valid && accepting && current) onQueue(stake);
      }}
    >
      <label>
        {text('blackjack.baseStake')}
        <input
          type="number"
          inputMode="decimal"
          min={config.min_stake}
          max={config.max_stake}
          step={config.stake_step}
          value={stake}
          onChange={(e) => setStake(e.target.value)}
          disabled={blocked}
          aria-invalid={!valid}
        />
      </label>
      <GameActionBar cost={stake}>
        <button
          className="btn btn-primary"
          type="submit"
          disabled={blocked || !accepting || !valid || !current}
        >
          {accepting ? text('blackjack.joinQueue') : text('blackjack.gameClosed')}
        </button>
      </GameActionBar>
      {!!config.quick_stakes?.length && (
        <div
          className="bj-quick-stakes"
          role="group"
          aria-label={text('blackjack.quickStakeSelection')}
        >
          {config.quick_stakes.map((amount) => (
            <button
              key={amount}
              type="button"
              className="btn btn-secondary"
              disabled={blocked}
              aria-pressed={selected === creditsToMilli(amount)}
              onClick={() => setStake(amount)}
            >
              {formatCredits(amount)}
            </button>
          ))}
        </div>
      )}
      {!current && <p role="status">{text('blackjack.refreshingStakeConfiguration')}</p>}
      {!valid && <p role="status">{text('blackjack.chooseAStakeWithinTheCurrentLimits')}</p>}
    </form>
  );
}
export function BlackjackGame() {
  const text = useDuelText();
  const game = useBlackjack();
  const snapshot = useGamesSnapshot();
  const home = game.query.data;
  const audioFacts = useSnapshotAudioFacts(home, blackjackAudioFacts);
  const audio = useArcadeAudio('blackjack', {
    facts: audioFacts,
    now: (home?.server_now ?? 0) * 1000,
    ready: !!home,
  });
  const sound = audio.sound;
  const surface = useTableMotion(home);
  const [panel, setPanel] = useState<'rules' | 'history' | null>(null);
  const close = useCallback(() => setPanel(null), []);
  const remaining = useAuthoritativeCountdown(
    `${home?.next_round_at}:${home?.phase}`,
    home?.deadline ?? null,
    home?.server_now ?? 0,
    game.refresh,
  );
  const accepting = !!home?.config.enabled && !!snapshot.data?.gamesEnabled && !snapshot.error;
  const own = home?.you;
  const actions =
    own?.state === 'playing' && own.session_id === home?.table?.id ? own.legal_actions : {};
  const actionLabel = (action: BlackjackAction) =>
    ({
      hit: text('blackjack.hit'),
      stand: text('blackjack.stand'),
      double: text('blackjack.double'),
      split: text('blackjack.split'),
    })[action];
  const controls = home && own?.state === 'playing' && (
    <section className="bj-controls" aria-label={text('blackjack.handActions')}>
      {Object.entries(actions)
        .filter(([, legal]) => legal.length > 0)
        .map(([hand, legal]) => (
          <div key={hand}>
            <strong>
              {text('blackjack.hand')} {Number(hand) + 1}
            </strong>
            <div className="duel-actions">
              {BLACKJACK_ACTIONS.filter((a) => legal.includes(a)).map((action) => (
                <button
                  key={action}
                  type="button"
                  className={`btn ${action === 'hit' ? 'btn-primary' : 'btn-secondary'}`}
                  disabled={game.blocked || own.pending || remaining === 0}
                  onClick={() => {
                    const h = home.table?.fact.cards?.seats.find((s) => s.number === own.seat)
                      ?.hands[Number(hand)];
                    if (h && own.session_id) {
                      sound.play('common_select');
                      game.run({
                        kind: 'action',
                        id: own.session_id,
                        hand: Number(hand),
                        revision: h.revision,
                        action,
                      });
                    }
                  }}
                >
                  {actionLabel(action)}
                  {(action === 'double' || action === 'split') && (
                    <small> +{formatCredits(own.stake)}</small>
                  )}
                </button>
              ))}
            </div>
          </div>
        ))}
      <p aria-live="polite">
        {own.pending
          ? text('blackjack.actionAcceptedWaitingForThisSecondS')
          : Object.values(actions).every((a) => !a.length)
            ? text('blackjack.yourDecisionsAreCompleteWaitingForThe')
            : text('blackjack.oneActionPerSecondUnfinishedHandsStand')}
      </p>
    </section>
  );
  return (
    <main className="game-page">
      <div ref={surface} className="bidding-game blackjack-game">
        <header className="bid-heading">
          <div>
            <GameBackLink />
            <h1>{text('blackjack.blackjack')}</h1>
            <p>{text('blackjack.oneTableYourOwnHandAgainstThe')}</p>
          </div>
          <div className="duel-actions">
            {snapshot.data && <GameWallets wallets={snapshot.data} />}
            <GameHeaderTool
              icon="?"
              label={text('bidding.rules')}
              onClick={() => setPanel('rules')}
            />
            <GameHeaderTool
              icon="◷"
              label={text('blackjack.history')}
              onClick={() => setPanel('history')}
            />
            <ArcadeAudioControls compact sound={sound} unavailable={audio.unavailable} />
            <a
              className="btn btn-secondary game-header-tool"
              href="#game-rankings"
              aria-label={text('ranking.leaderboards')}
              title={text('ranking.leaderboards')}
            >
              <span aria-hidden="true">▥</span>
            </a>
          </div>
        </header>
        {snapshot.data && (
          <OnboardingCard game="blackjack" progress={snapshot.data.onboarding.blackjack} />
        )}
        <RandomnessProof
          game="blackjack"
          id={home?.table?.id}
          terminal={home?.table?.phase === 'result' || home?.table?.phase === 'cancelled'}
        />
        {game.query.isPending ? (
          <LoadingState />
        ) : game.query.error ? (
          <ErrorState error={game.query.error} onRetry={game.refresh} />
        ) : null}
        {home && (
          <>
            <section className="bj-timing" aria-label={text('blackjack.roundPhase')}>
              <ol>
                {(['seating', 'decision', 'result'] as const).map((phase, i) => (
                  <li
                    key={phase}
                    aria-current={
                      (home.phase === 'cancelled' ? 'result' : home.phase) === phase
                        ? 'step'
                        : undefined
                    }
                  >
                    <span>0{i + 1}</span>
                    {
                      [
                        text('blackjack.seating'),
                        text('blackjack.decisions'),
                        text('blackjack.results'),
                      ][i]
                    }
                    <small>{[5, 20, 5][i]}s</small>
                  </li>
                ))}
              </ol>
              <strong
                className={`bid-clock ${remaining !== null && remaining <= 5 ? 'is-urgent' : ''}`}
                role="timer"
                aria-label={text('blackjack.secondsRemaining')}
              >
                {remaining ?? '—'}
                <small>s</small>
              </strong>
            </section>
            <p className="bid-hint">
              {text('blackjack.nextRound')}{' '}
              {new Date(home.next_round_at * 1000).toLocaleTimeString()} ·{' '}
              {text('blackjack.waiting')} {home.queue_count}
            </p>
            {home.table ? (
              <BlackjackBoard table={home.table} ownSeat={home.your_seat} controls={controls} />
            ) : (
              <div className="bj-idle">
                <span aria-hidden="true">A ♠</span>
                <h2>{text('blackjack.theTableIsWaiting')}</h2>
                <p>{text('blackjack.onePlayerIsEnoughNineSeatsMaximum')}</p>
              </div>
            )}
            {home.table?.phase === 'result' && (
              <BlackjackSettlement fact={home.table.fact} seat={home.your_seat} />
            )}
            {home.table?.phase === 'cancelled' && (
              <section className="bj-settlement" data-settlement>
                <h2>{text('blackjack.tableCancelled')}</h2>
                <p>{text('blackjack.everyStakeWasRefundedInItsOriginal')}</p>
              </section>
            )}
            <section className="bj-queue" aria-label={text('blackjack.queueAndSeating')}>
              {own ? (
                <>
                  <h2>
                    {own.state === 'waiting'
                      ? `${text('blackjack.queuePosition')} #${own.position}`
                      : own.state === 'seated'
                        ? `${text('blackjack.seated')} #${(own.seat ?? 0) + 1}`
                        : text('blackjack.playingThisRound')}
                  </h2>
                  <p>
                    {text('blackjack.baseStake')} {formatCredits(own.stake)} ·{' '}
                    {text('blackjack.gameCreditsHeld')} {formatCredits(own.payment.game)} ·{' '}
                    {text('blackjack.generalCredits')} {formatCredits(own.payment.general)}
                  </p>
                  <p>
                    {text('blackjack.frozenFees')} {text('blackjack.platform')}{' '}
                    {own.rake_bp.platform / 100}% · {text('blackjack.welfare')}{' '}
                    {own.rake_bp.welfare / 100}% · {text('blackjack.thursday')}{' '}
                    {own.rake_bp.thursday / 100}%
                  </p>
                  {own.state !== 'playing' && (
                    <button
                      type="button"
                      className="btn btn-secondary"
                      disabled={game.blocked || (own.state === 'seated' && remaining === 0)}
                      onClick={() => game.run({ kind: 'leave', id: own.id })}
                    >
                      {text('blackjack.leaveAndRefundOriginalCredits')}
                    </button>
                  )}
                  <p className="bid-hint">
                    {own.state === 'waiting'
                      ? text('blackjack.yourPlaceRemainsValidAcrossRoundsYou')
                      : text('blackjack.joinTheQueueAgainAfterThisTable')}
                  </p>
                </>
              ) : (
                <>
                  <h2>{text('blackjack.yourNextHandIsYourChoice')}</h2>
                  <QueueForm
                    config={home.config}
                    blocked={game.blocked}
                    accepting={accepting}
                    onQueue={(stake) =>
                      game.run({ kind: 'queue', stake, config_hash: home.config_hash })
                    }
                  />
                  <p>
                    {formatCredits(home.config.min_stake)}–{formatCredits(home.config.max_stake)} ·{' '}
                    {text('blackjack.step')} {formatCredits(home.config.stake_step)} ·{' '}
                    {text('blackjack.totalFeesOnEachReturn')}{' '}
                    {(home.config.rake_bp.platform +
                      home.config.rake_bp.welfare +
                      home.config.rake_bp.thursday) /
                      100}
                    %
                  </p>
                  <p className="bid-hint">
                    {text('blackjack.joiningReservesYourStakeGameCreditsFirst')}
                  </p>
                </>
              )}
            </section>
            {home.your_seat !== null && home.table && home.phase !== 'cancelled' && (
              <div className="bj-emotes" role="group" aria-label={text('blackjack.presetEmotes')}>
                {BLACKJACK_EMOTES.map((emote) => (
                  <button
                    className="btn btn-secondary"
                    key={emote}
                    disabled={game.blocked}
                    onClick={() => {
                      if (home.table) game.run({ kind: 'emote', id: home.table.id, emote });
                    }}
                  >
                    {emoteText(emote, text)}
                  </button>
                ))}
              </div>
            )}
          </>
        )}
        {!!game.error && <ErrorState error={game.error} />}
        {game.uncertain && (
          <button className="btn btn-primary" disabled={game.pending} onClick={game.retry}>
            {text('blackjack.confirmPreviousAction')}
          </button>
        )}
        {panel === 'rules' && <Rules close={close} />}
        {panel === 'history' && <History close={close} />}
        <div id="game-rankings">
          <LeaderboardTabs
            items={[
              {
                id: 'net-profit',
                label: text('blackjack.cardMasterLeaderboard'),
                content: <Leaderboard board="blackjack_net_profit" />,
              },
              {
                id: 'profit',
                label: text('blackjack.profitLeaderboard'),
                content: <Leaderboard board="blackjack" />,
              },
            ]}
          />
        </div>
      </div>
    </main>
  );
}
