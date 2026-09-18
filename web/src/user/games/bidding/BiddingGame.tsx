import { useCallback, useState } from 'react';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { GameWallets } from '../common/GameWallets';
import { RandomnessProof } from '../common/RandomnessProof';
import { GamePayment } from '../common/GamePayment';
import { useAuthoritativeCountdown } from '../common/countdown';
import { useDuel } from '../common/duel/api';
import { useDuelText } from '../common/duel/copy';
import { DuelDialog } from '../common/duel/Dialog';
import { DuelFeedback, DuelProfile } from '../common/duel/Feedback';
import { DuelFinance, DuelTerms } from '../common/duel/Finance';
import { DuelHistory } from '../common/duel/History';
import type { DuelLobbyContext } from '../common/duel/types';
import { creditsToMilli, formatCredits } from '../common/strict';
import { BIDDING_MODES, biddingCodec } from './normalize';
import { BiddingControls, PublicCards, RewardCard } from './Cards';
import { BiddingRoundView, PlayedHistory } from './HistoryView';
import { biddingAudioFacts } from './audioFacts';
import { useArcadeAudio } from '../common/audio/useArcadeAudio';
import { useSnapshotAudioFacts } from '../common/audio/useSnapshotAudioFacts';
import { ArcadeAudioControls } from '../common/audio/ArcadeAudioControls';
import { BiddingPresentation } from './BiddingPresentation';
import '../games.css';
import '../common/duel/duel.css';
import './bidding.css';

function BiddingRules({ onClose }: { readonly onClose: () => void }) {
  const t = useDuelText();
  return (
    <DuelDialog title={t('竞标对决 · 规则', 'Bidding Duel · Rules')} onClose={onClose}>
      <h3>{t('十三轮，把握每一张牌', 'Thirteen rounds. Make every card count.')}</h3>
      <p>
        {t(
          '双方各有A至K共13张出价牌，点数为1至13；每张整局只能使用一次。每轮翻开红心和黑桃奖励各一张，双方同时暗选出价。较大者取得整个奖池的分数，出价牌本身不计分。',
          'Each player has thirteen bidding cards, A through K, valued 1–13. Each card is used once. Every round reveals one heart and one spade reward. Both players bid privately; the higher bid claims the entire pool. Bidding cards do not score points themselves.',
        )}
      </p>
      <h3>{t('平手与累计', 'Ties and carry')}</h3>
      <p>
        {t(
          '平手时奖励留在奖池，下一轮继续争夺。第13轮仍平手，奖池全部丢弃。最终总分更高的一方获胜，总分相同则平局。',
          'A tied bid carries the rewards into the next round. A tie in round thirteen discards the remaining pool. The higher final total wins; equal totals draw.',
        )}
      </p>
      <h3>{t('王的时机', 'When to use your joker')}</h3>
      <p>
        {t(
          '前12轮轮流拥有王的决策权，每人6次机会。每人整局只能使用一次王，使本轮己方花色奖励翻倍。决策阶段10秒，超时视为保留；第13轮没有王阶段。出价阶段双方各有完整20秒，超时未锁定时自动使用剩余最小牌。',
          'During the first twelve rounds, joker decisions alternate, giving each player six opportunities. Each player may use their joker once to double their suit’s current reward. The decision lasts ten seconds; timeout saves the joker. Round thirteen has no joker decision. Each bidding phase lasts twenty seconds; an unlocked player times out with their lowest remaining card.',
        )}
      </p>
      <h3>{t('匹配与结算', 'Matching and settlement')}</h3>
      <p>
        {t(
          '排队最多120秒，可随时取消；匹配后入场条款固定。认输立即判负。费用只在胜负局从输家投入扣取，胜者本金原币返还并获得通用积分奖金；平局或系统取消双方原额退款。完整对局记录保留30天。',
          'Queue for up to 120 seconds and cancel at any time before matching. Entry terms are fixed once queued. Surrender immediately concedes the game. Fees are charged from the losing entry; the winner gets their original principal and a general-credit prize. Draws and system cancellations refund both players. Complete game records are kept for thirty days.',
        )}
      </p>
    </DuelDialog>
  );
}
export function BiddingGame({ config, wallets, accepting, refreshWallets }: DuelLobbyContext) {
  const t = useDuelText();
  const duel = useDuel(biddingCodec, refreshWallets);
  const [mode, setMode] = useState<string>(
    BIDDING_MODES.find((key) => config.modes[key]?.available) ?? 'tier1',
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
    creditsToMilli(wallets.balance) + creditsToMilli(wallets.gameBalance) >=
      creditsToMilli(selected.ticket);
  const unavailable = !accepting || !config.available || !selected?.available;
  return (
    <div className="bidding-game">
      <header className="bid-heading">
        <div>
          <span className="bid-eyebrow">A — K · 13</span>
          <h1>{t('竞标对决', 'Bidding Duel')}</h1>
          <p>{t('留一手，赢下整个奖池。', 'Hold your nerve. Take the whole pool.')}</p>
        </div>
        <div className="duel-actions">
          <ArcadeAudioControls sound={audio.sound} unavailable={audio.unavailable} />
          <button type="button" className="btn btn-secondary" onClick={() => setRules(true)}>
            {t('游戏规则', 'Rules')}
          </button>
          <button type="button" className="btn btn-secondary" onClick={() => setHistory(true)}>
            {t('对局记录', 'Game history')}
          </button>
        </div>
      </header>
      <GameWallets wallets={wallets} />
      <RandomnessProof
        game="bidding"
        id={current?.id ?? home?.latestResult?.id}
        terminal={!current && !!home?.latestResult}
      />
      <DuelFeedback
        error={duel.error ?? duel.query.error}
        pending={duel.pending || duel.query.isPending}
        uncertain={duel.uncertain}
        onRetry={duel.uncertain ? duel.retry : duel.refresh}
      />
      <BiddingPresentation home={home} onCue={audio.sound.play} />
      {current ? (
        <>
          <div className="bid-phase">
            <strong>
              {t('第', 'Round')} {current.round} / 13 {t('轮', '')}
            </strong>
            <span>
              {current.phase === 'joker'
                ? t('王的选择', 'Joker decision')
                : t('共同暗选', 'Simultaneous bidding')}
            </span>
            <span
              className={`bid-clock ${(remaining ?? 0) <= 5 ? 'is-urgent' : ''}`}
              aria-label={t('剩余秒数', 'Seconds remaining')}
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
                  <small>{t('分', 'pts')}</small>
                </strong>
                <span>
                  {current.view.jokers[seat]
                    ? `♛ ${t('王可用', 'Joker available')}`
                    : `♛ ${t('王已用', 'Joker used')}`}
                </span>
                <span className="bid-lock">
                  {current.locked[seat] ? t('已锁定', 'Locked') : t('选择中', 'Choosing')}
                </span>
              </section>
            ))}
          </div>
          {current.round === 1 && (
            <p className="bid-matched">
              {t(
                '匹配成功。双方独立选择，同时揭晓。',
                'Match found. Choose independently; reveal together.',
              )}
            </p>
          )}
          <section className="bid-table" aria-label={t('本轮奖励与奖池', 'Round rewards and pool')}>
            <div className="bid-rewards">
              {current.view.rewards
                .filter((card) => card.round === current.round)
                .map((card) => (
                  <RewardCard key={`${card.round}:${card.side}`} card={card} />
                ))}
            </div>
            <div className="bid-pool">
              <span>{t('当前奖池', 'CURRENT POOL')}</span>
              <strong key={`${current.round}:${current.view.pool}`}>{current.view.pool}</strong>
              <small>{t('分 · 出价更高者全取', 'points · winner takes all')}</small>
              {current.view.rewards.some(
                (card) => card.round < current.round && card.status === 'pool',
              ) && (
                <p className="bid-carry">
                  {t('包含此前平手累计奖励', 'Includes rewards carried from earlier ties')}
                </p>
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
              className="btn btn-secondary"
              disabled={duel.blocked}
              onClick={() => setSurrender(true)}
            >
              {t('认输', 'Surrender')}
            </button>
            <small>
              {t('本局投入', 'Your entry')}: <GamePayment payment={current.payment} />
            </small>
          </div>
        </>
      ) : queue ? (
        <section className="bid-lobby">
          <span className="bid-eyebrow">{t('寻找对手', 'FINDING A MATCH')}</span>
          <h2>{t('牌桌已就绪', 'Your seat is ready')}</h2>
          <p>
            {t('排队剩余', 'Queue time left')}: <strong>{remaining ?? '—'}s</strong>
          </p>
          <GamePayment payment={queue.payment} />
          <div className="duel-actions">
            <button
              type="button"
              className="btn btn-secondary"
              disabled={duel.blocked}
              onClick={() => duel.run({ kind: 'cancel', id: queue.id, revision: queue.revision })}
            >
              {t('取消排队并退款', 'Cancel queue and refund')}
            </button>
          </div>
        </section>
      ) : (
        <>
          {home?.latestResult && (
            <section className="bid-result">
              <DuelFinance result={home.latestResult} />
              {home.latestResult.view && (
                <>
                  <PlayedHistory view={home.latestResult.view} you={home.latestResult.you} />
                  {home.latestResult.view.rewards.some((card) => card.status === 'discarded') && (
                    <p className="bid-carry">
                      {t(
                        '最后一轮平手，剩余奖池已丢弃。',
                        'The final round tied. The remaining pool was discarded.',
                      )}
                    </p>
                  )}
                </>
              )}
            </section>
          )}
          <section className="bid-lobby">
            <span className="bid-eyebrow">{t('选择牌桌', 'CHOOSE YOUR TABLE')}</span>
            <h2>{t('十三张牌，一次对决', 'Thirteen cards. One duel.')}</h2>
            <div className="bid-modes" role="group" aria-label={t('入场档位', 'Entry tier')}>
              {BIDDING_MODES.map((key, index) => (
                <button
                  key={key}
                  type="button"
                  aria-pressed={mode === key}
                  disabled={duel.pending || duel.uncertain}
                  onClick={() => setMode(key)}
                >
                  <span>
                    {t('第', 'Tier ')} {index + 1} {t('档', '')}
                  </span>
                  <strong>{formatCredits(config.modes[key].ticket)}</strong>
                  <small>
                    {config.modes[key].available ? t('开放', 'Open') : t('暂未开放', 'Unavailable')}
                  </small>
                </button>
              ))}
            </div>
            {selected && <DuelTerms mode={selected} />}
            <button
              type="button"
              className="btn btn-primary"
              disabled={duel.blocked || unavailable || !enough}
              onClick={() => duel.run({ kind: 'queue', mode, termsHash: selected.termsHash })}
            >
              {unavailable
                ? t('暂时无法入场', 'Entry unavailable')
                : !enough
                  ? t('可用积分不足', 'Insufficient credits')
                  : t('支付票价并匹配', 'Pay entry and find a match')}
            </button>
          </section>
        </>
      )}
      {rules && <BiddingRules onClose={closeRules} />}
      {history && (
        <DuelHistory
          codec={biddingCodec}
          onClose={closeHistory}
          renderRound={(round, you) => <BiddingRoundView round={round} you={you} />}
        />
      )}
      <ConfirmDialog
        open={surrender && !!current}
        title={t('确认认输', 'Confirm surrender')}
        description={t(
          '认输将立即判负，入场积分不退还。',
          'Surrender immediately concedes the game. Your entry will not be refunded.',
        )}
        confirmLabel={t('确认认输', 'Surrender')}
        cancelLabel={t('继续对局', 'Keep playing')}
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
