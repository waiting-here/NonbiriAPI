import { BiddingLobby, AIMatchInfo, AIActionLog } from './AIPlayers';
import { useAIText } from '@shared/aiPlayers';
import { GameToolbar } from '../common/GameToolbar';
import { GameMoney } from '../common/GameMoney';
import { GameBackLink } from '../common/GameBackLink';
import { GameActionBar } from '../common/GameActionBar';
import { useCallback, useState } from 'react';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { Fold } from '@shared/components/ui/Fold';
import { GameWallets } from '../common/GameWallets';
import { Leaderboard } from '../ranking/Leaderboard';
import { LeaderboardTabs } from '../ranking/LeaderboardTabs';
import { OnboardingCard } from '../common/OnboardingCard';
import { RandomnessProof } from '../common/RandomnessProof';
import { GamePayment } from '../common/GamePayment';
import { useAuthoritativeCountdown } from '../common/countdown';
import { useDuel } from '../common/duel/api';
import { useDuelText } from '../common/duel/copy';
import { DuelDialog } from '../common/duel/Dialog';
import { DuelFeedback, DuelProfile } from '../common/duel/Feedback';
import { entryMessage, entryProblem } from '../common/duel/availability';
import { DuelFinance, DuelTerms } from '../common/duel/Finance';
import { DuelHistory } from '../common/duel/History';
import type { DuelLobbyContext } from '../common/duel/types';
import { creditsToMilli, formatCredits } from '../common/strict';
import { spendableGameCredits } from '../common/spendable';
import { BIDDING_MODES, biddingCodec } from './normalize';
import { BiddingControls, PublicCards, RewardCard, RewardDeck } from './Cards';
import { BiddingRoundView, PlayedHistory } from './HistoryView';
import { biddingAudioFacts } from './audioFacts';
import { useArcadeAudio } from '../common/audio/useArcadeAudio';
import { useSnapshotAudioFacts } from '../common/audio/useSnapshotAudioFacts';
import { BiddingPresentation } from './BiddingPresentation';
import '../games.css';
import '../common/duel/duel.css';
import './bidding.css';
function BiddingRules({ onClose }: { readonly onClose: () => void }) {
  const text = useDuelText();
  return (
    <DuelDialog title={text('bidding.biddingDuelRules')} onClose={onClose}>
      <h3>{text('bidding.thirteenRoundsMakeEveryCardCount')}</h3>
      <p>{text('bidding.theRedSideBidsWithHeartsAnd')}</p>
      <h3>{text('bidding.tiesAndCarry')}</h3>
      <p>{text('bidding.aTiedBidCarriesTheRewardsInto')}</p>
      <h3>{text('bidding.whenToUseYourJoker')}</h3>
      <p>{text('bidding.duringTheFirstTwelveRoundsJokerDecisions')}</p>
      <h3>{text('bidding.matchingAndSettlement')}</h3>
      <p>{text('bidding.queueForUpTo120SecondsAnd')}</p>
    </DuelDialog>
  );
}
export function BiddingGame({
  config,
  wallets,
  onboarding,
  accepting,
  refreshWallets,
}: DuelLobbyContext) {
  const text = useDuelText();
  const aiText = useAIText();
  const duel = useDuel(biddingCodec, refreshWallets);
  const [mode, setMode] = useState<string>(
    BIDDING_MODES.find((key) => !entryProblem({ config, accepting }, key)) ?? 'tier1',
  );
  const [rules, setRules] = useState(false),
    [history, setHistory] = useState(false),
    [surrender, setSurrender] = useState(false);
  const closeRules = useCallback(() => setRules(false), []),
    closeHistory = useCallback(() => setHistory(false), []);
  const home = duel.query.data,
    current = home?.current,
    queue = home?.queue;
  const audioFacts = useSnapshotAudioFacts(home, biddingAudioFacts);
  const audio = useArcadeAudio('bidding', {
    facts: audioFacts.filter(
      (fact) => !['bidding_reveal', 'bidding_pot_add', 'bidding_pot_collect'].includes(fact.cue),
    ),
    now: (home?.serverNow ?? 0) * 1000,
    ready: !!home,
  });
  const remaining = useAuthoritativeCountdown(
    current ? `${current.id}:${current.phaseSeq}` : (queue?.id ?? 'idle'),
    current?.deadline ?? queue?.deadline ?? null,
    home?.serverNow ?? 0,
    duel.refresh,
  );
  const selected = config.modes[mode];
  const enough =
    selected &&
    creditsToMilli(spendableGameCredits(wallets).total) >= creditsToMilli(selected.ticket);
  const unavailable = entryProblem({ config, accepting }, mode);
  return (
    <div className="bidding-game">
      <header className="bid-heading">
        <div>
          <GameBackLink />
          <h1>{text('bidding.biddingDuel')}</h1>
          <p>{text('bidding.holdYourNerveTakeTheWholePool')}</p>
        </div>
        <div className="duel-actions">
          <GameWallets wallets={wallets} />
          <GameToolbar
            sound={audio.sound}
            audioUnavailable={audio.unavailable}
            items={[
              {
                id: 'rules',
                label: text('bidding.rules'),
                icon: 'help',
                onClick: () => setRules(true),
              },
              {
                id: 'history',
                label: text('bidding.gameHistory'),
                icon: 'history',
                onClick: () => setHistory(true),
              },
              {
                id: 'rankings',
                label: text('ranking.leaderboards'),
                icon: 'trophy',
                href: '#game-rankings',
              },
            ]}
          />
        </div>
      </header>
      {onboarding && <OnboardingCard game="bidding" progress={onboarding} />}
      <RandomnessProof
        game="bidding"
        id={current?.id ?? home?.latestResult?.id}
        terminal={!current && !!home?.latestResult}
      />
      <DuelFeedback
        queueAttempt={duel.intentKind === 'queue'}
        entryProblem={unavailable}
        error={duel.error ?? duel.query.error}
        pending={duel.pending || duel.query.isPending}
        uncertain={duel.uncertain}
        onRetry={duel.uncertain ? duel.retry : duel.refresh}
      />
      {home?.aiQueueError && (
        <p role="status" className="duel-feedback">
          {home.aiQueueError === 'insufficient_credits'
            ? aiText(
                '入场时余额不足，排队已结束，未扣除门票。',
                'Insufficient balance at admission. The queue ended without charging a ticket.',
              )
            : aiText(
                '人机排队已结束，未扣除门票。请重新选择对手。',
                'The AI queue ended without charging a ticket. Please choose an opponent again.',
              )}
        </p>
      )}
      {current?.ai && <AIMatchInfo ai={current.ai} sources={current.sources} />}
      <BiddingPresentation home={home} onCue={audio.sound.play} />
      {current ? (
        <div className="bid-play">
          <div className="bid-phase">
            <strong>
              {text('bidding.round')} {current.round} / 13 {text('bidding.message')}
            </strong>
            <span>
              {current.phase === 'joker'
                ? text('bidding.jokerDecision')
                : text('bidding.simultaneousBidding')}
            </span>
            <span
              className={`bid-clock ${(remaining ?? 0) <= 5 ? 'is-urgent' : ''}`}
              aria-label={text('bidding.secondsRemaining')}
            >
              {remaining ?? '—'}
              <small>s</small>
            </span>
          </div>
          <div className="bid-scoreboard">
            {[current.you, (1 - current.you) as 0 | 1].map((seat) => (
              <section key={seat}>
                <DuelProfile profile={current.profiles[seat]} you={seat === current.you} />
                <strong key={`${current.round}:${current.view.scores[seat]}`}>
                  {current.view.scores[seat]}
                  <small>{text('bidding.pts')}</small>
                </strong>
                <span>
                  {current.view.jokers[seat]
                    ? `♛ ${text('bidding.jokerAvailable')}`
                    : `♛ ${text('bidding.jokerUsed')}`}
                </span>
                <span className="bid-lock">
                  {current.locked[seat] ? text('bidding.locked') : text('bidding.choosing')}
                </span>
              </section>
            ))}
          </div>
          {current.round === 1 && (
            <p className="bid-matched">
              {text('bidding.matchFoundChooseIndependentlyRevealTogether')}
            </p>
          )}
          <section className="bid-table" aria-label={text('bidding.roundRewardsAndPool')}>
            <RewardDeck
              view={current.view}
              side={current.you}
              round={current.round}
              you={current.you}
            />
            <div className="bid-rewards">
              {current.view.rewards
                .filter((card) => card.round === current.round)
                .sort((a, b) => (a.side === current.you ? -1 : b.side === current.you ? 1 : 0))
                .map((card) => (
                  <RewardCard key={`${card.round}:${card.side}`} card={card} />
                ))}
            </div>
            <RewardDeck
              view={current.view}
              side={(1 - current.you) as 0 | 1}
              round={current.round}
              you={current.you}
            />
            <div className="bid-pool">
              <span>{text('bidding.cURRENTPOOL')}</span>
              <strong key={`${current.round}:${current.view.pool}`}>{current.view.pool}</strong>
              <small>{text('bidding.pointsWinnerTakesAll')}</small>
              {current.view.rewards.some(
                (card) => card.round < current.round && card.status === 'pool',
              ) && (
                <p className="bid-carry">{text('bidding.includesRewardsCarriedFromEarlierTies')}</p>
              )}
            </div>
          </section>
          <BiddingControls
            onSelect={() => audio.sound.play('common_select')}
            key={`${current.id}:${current.phaseSeq}`}
            state={current}
            blocked={duel.blocked || remaining === 0}
            onAction={(action) =>
              duel.run({ kind: 'action', id: current.id, phaseSeq: current.phaseSeq, action })
            }
          />
          <PublicCards view={current.view} you={current.you} />
          <PlayedHistory view={current.view} you={current.you} />
          <div className="duel-actions">
            <button
              type="button"
              className="nb-btn nb-btn--secondary"
              disabled={duel.blocked}
              onClick={() => setSurrender(true)}
            >
              {text('bidding.surrender')}
            </button>
            <div>
              <span>{text('bidding.yourEntry')}</span>
              <GamePayment payment={current.payment} />
            </div>
          </div>
        </div>
      ) : queue ? (
        <section className="bid-lobby">
          <span className="bid-eyebrow">
            {queue.ai
              ? aiText('等待人机对局', 'WAITING FOR AI MATCH')
              : text('bidding.fINDINGAMATCH')}
          </span>
          <h2>{queue.ai?.bot_name ?? text('bidding.yourSeatIsReady')}</h2>
          <p>
            {text('bidding.queueTimeLeft')}: <strong>{remaining ?? '—'}s</strong>
          </p>
          {queue.ai ? (
            <p>
              {aiText(
                `前方包含自己共 ${queue.position ?? 1} 位。入场前不扣门票。`,
                `Queue position: ${queue.position ?? 1}. No ticket is charged before admission.`,
              )}
            </p>
          ) : (
            <GamePayment payment={queue.payment} />
          )}
          <div className="duel-actions">
            <button
              type="button"
              className="nb-btn nb-btn--secondary"
              disabled={duel.blocked}
              onClick={() => duel.run({ kind: 'cancel', id: queue.id, revision: queue.revision })}
            >
              {queue.ai ? aiText('取消排队', 'Leave queue') : text('bidding.cancelQueueAndRefund')}
            </button>
          </div>
        </section>
      ) : (
        <>
          <BiddingLobby
            blocked={duel.blocked}
            onStart={duel.run}
            lastMatch={home?.latestResult?.id}
          >
            <section className="bid-lobby">
              <span className="bid-eyebrow">{text('bidding.cHOOSEYOURTABLE')}</span>
              <h2>{text('bidding.thirteenCardsOneDuel')}</h2>
              <div className="bid-modes" role="group" aria-label={text('bidding.entryTier')}>
                {BIDDING_MODES.map((key, index) => (
                  <button
                    key={key}
                    type="button"
                    aria-pressed={mode === key}
                    disabled={
                      duel.pending || duel.uncertain || !!entryProblem({ config, accepting }, key)
                    }
                    onClick={() => setMode(key)}
                  >
                    <span>
                      {text('bidding.tier')} {index + 1} {text('bidding.message2')}
                    </span>
                    <strong>{formatCredits(config.modes[key]?.ticket ?? '0')}</strong>
                    <small>
                      {!entryProblem({ config, accepting }, key)
                        ? text('bidding.open')
                        : text('bidding.unavailable')}
                    </small>
                  </button>
                ))}
              </div>
              {selected && <DuelTerms mode={selected} />}
              <GameActionBar cost={<GameMoney value={selected?.ticket ?? '0'} />}>
                <button
                  type="button"
                  className="nb-btn nb-btn--primary"
                  disabled={duel.blocked || !!unavailable || !enough}
                  onClick={() => duel.run({ kind: 'queue', mode, termsHash: selected.termsHash })}
                >
                  {unavailable
                    ? entryMessage(unavailable, text)
                    : !enough
                      ? text('bidding.insufficientCredits')
                      : text('bidding.payEntryAndFindAMatch')}
                </button>
              </GameActionBar>
            </section>
          </BiddingLobby>
          {home?.latestResult && (
            <section className="bid-result">
              <DuelFinance result={home.latestResult} />
              {home.latestResult.view && (
                <Fold title={text('bidding.viewPlayedHistory')}>
                  <PlayedHistory view={home.latestResult.view} you={home.latestResult.you} />
                  {home.latestResult.view.rewards.some((card) => card.status === 'discarded') && (
                    <p className="bid-carry">{text('bidding.theFinalRoundTiedTheRemainingPool')}</p>
                  )}
                </Fold>
              )}
            </section>
          )}
        </>
      )}
      {rules && <BiddingRules onClose={closeRules} />}
      <div id="game-rankings">
        <LeaderboardTabs
          items={[
            {
              id: 'net-profit',
              label: text('bidding.biddingMasters'),
              content: <Leaderboard board="bidding_net_profit" />,
            },
            {
              id: 'profit',
              label: text('bidding.biddingProfits'),
              content: <Leaderboard board="bidding" />,
            },
          ]}
        />
      </div>
      {history && (
        <DuelHistory
          codec={biddingCodec}
          onClose={closeHistory}
          renderDetail={(detail) => <AIActionLog sources={detail.sources} />}
          renderRound={(round, you) => <BiddingRoundView round={round} you={you} />}
        />
      )}
      <ConfirmDialog
        open={surrender && !!current}
        title={text('bidding.confirmSurrender')}
        description={text('bidding.surrenderImmediatelyConcedesTheGameYourEntry')}
        confirmLabel={text('bidding.surrender2')}
        cancelLabel={text('bidding.keepPlaying')}
        danger
        busy={duel.pending}
        onCancel={() => setSurrender(false)}
        onConfirm={() => {
          if (current) duel.run({ kind: 'surrender', id: current.id, phaseSeq: current.phaseSeq });
          setSurrender(false);
        }}
      />
    </div>
  );
}
