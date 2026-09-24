import { useCallback, useEffect, useRef, useState } from 'react';
import { Link } from 'react-router';
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
  const t = useDuelText();
  return (
    <DuelDialog title={t('二十一点 · 桌规', 'Blackjack · Table rules')} onClose={close}>
      <h3>{t('每30秒一局，最多九人', 'One round every 30 seconds, up to nine seats')}</h3>
      <p>
        {t(
          '每30秒开始一局，前5秒落座，随后20秒同时决策，最后5秒展示结果。全桌提前结束会延长展示，下一局仍按原定时间开始。无人不产生牌局。',
          'Every 30 seconds: 5 seconds for seating, 20 for simultaneous decisions and 5 for results. An early finish extends the result display; the next round keeps its scheduled start. An empty table creates no game.',
        )}
      </p>
      <p>
        {t(
          '入队立即预留基础投入，游戏积分优先，通用积分补足。按服务器受理顺序落座前九人；候补跨轮保留，随时退出原币退款。落座后在发牌前仍可退出并递补。玩完后需主动重新加入队尾，不保座、不自动续投。',
          'Joining reserves your stake, using game credits first and general credits for the remainder. The first nine accepted requests take seats. Waiting players keep their place across rounds and may leave for an original-asset refund. Seated players may leave before dealing. After playing, join the tail again explicitly; there is no automatic re-entry.',
        )}
      </p>
      <h3>{t('六副牌与庄家', 'Six decks and the dealer')}</h3>
      <p>
        {t(
          '每局重新洗完整六副牌。A按1或11，J/Q/K按10；庄家软17停牌。美式底牌预查：庄家自然二十一点会立即结束。原始两张A加10点牌为自然二十一点，优先普通21。没有保险、投降、五龙或旁注。',
          'Six complete decks are shuffled anew each round. Aces count as 1 or 11; face cards count as 10. The dealer stands on soft 17 and peeks for blackjack. An original ace plus a ten-value card is a natural blackjack and beats an ordinary 21. No insurance, surrender, five-card bonus or side bets.',
        )}
      </p>
      <h3>{t('要牌、停牌、加倍、分牌', 'Hit, stand, double and split')}</h3>
      <p>
        {t(
          '最多分一次为两手，同点值牌可分。普通分牌后可加倍；分A各补一张即停。加倍补同额投入，只再拿一张牌。分牌所得21按普通胜局处理。每秒每席处理一个动作，同批按座位顺序发牌，首座逐局轮换。其他人操作不会改变你的手牌版本。截止时未结束的手自动停牌。',
          'Split equal-value cards once into two hands. Doubling after a normal split is allowed; split aces receive one card each and stand. Doubling reserves an equal stake and draws exactly one card. A split 21 is an ordinary win. One action per seat is processed each second, in rotating seat order. Other players do not change your hand revision. Unfinished hands stand at the deadline.',
        )}
      </p>
      <h3>{t('返还与费用', 'Returns and fees')}</h3>
      <p>
        {t(
          '失败返还0，平局返还本金，普通胜局返还2倍，自然二十一点返还2.5倍。每手应返总额分别扣平台、低保池、周四池费用，再全部发为通用积分；费率在入队时冻结。默认三项各1%，投入1000时，平局到账970、普通胜局1940、自然二十一点2425。长期游玩的预期收益为负。',
          'Gross returns are zero for a loss, the stake for a push, twice the stake for a win and 2.5 times the stake for a natural. Platform, welfare and Thursday fees are each deducted per hand; all net proceeds are general credits. Rates freeze when you queue. At the default 1% each, a stake of 1,000 returns 970 on a push, 1,940 on a win and 2,425 on a natural. Long-term expected returns are negative.',
        )}
      </p>
      <p>
        {t(
          '断线或关页仍正常推进。服务器重启只取消尚未结算的当前局，按原积分组成全额退款；候补继续排队。已提交的正常结果不回滚。近期记录保留30天，随后只保留去身份的牌局事实。',
          'Disconnection does not pause the game. A restart cancels only the current unsettled table and refunds every original payment; waiting players keep their queue. Committed results stay final. Personal history is available for 30 days, followed by anonymous game facts.',
        )}
      </p>
    </DuelDialog>
  );
}
function History({ close }: { readonly close: () => void }) {
  const t = useDuelText();
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
    <DuelDialog title={t('二十一点 · 对局记录', 'Blackjack · History')} onClose={close}>
      <p>
        {t(
          '可查看本人最近30天的已结算牌局。阅读记录不暂停当前牌桌。',
          'Your settled tables from the last 30 days. Reading history does not pause the live table.',
        )}
      </p>
      {selected ? (
        <>
          <button className="btn btn-secondary" onClick={() => setSelected(null)}>
            {t('返回列表', 'Back to list')}
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
                <p>
                  {t(
                    '系统取消，全部投入已原币退款。',
                    'System cancellation: all original payments refunded.',
                  )}
                </p>
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
                        ? t('已原退', 'Refunded')
                        : `${t('通用到账', 'General paid')} ${formatCredits(h.net)}`}
                    </span>
                  </button>
                ))}
              </div>
              {!page.data.items.length && <p>{t('暂无对局记录。', 'No games yet.')}</p>}
            </>
          )}
          <nav className="duel-actions" aria-label={t('历史翻页', 'History pages')}>
            <button
              className="btn btn-secondary"
              disabled={cursors.length === 1}
              onClick={() => setCursors((v) => v.slice(0, -1))}
            >
              {t('上一页', 'Previous')}
            </button>
            <button
              className="btn btn-secondary"
              disabled={!page.data?.next_cursor}
              onClick={() => {
                if (page.data?.next_cursor) setCursors((v) => [...v, page.data.next_cursor]);
              }}
            >
              {t('下一页', 'Next')}
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
  const t = useDuelText();
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
        {t('基础投入', 'Base stake')}
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
      <button
        className="btn btn-primary"
        type="submit"
        disabled={blocked || !accepting || !valid || !current}
      >
        {accepting ? t('加入队列', 'Join queue') : t('游戏未开放', 'Game closed')}
      </button>
      {!!config.quick_stakes?.length && (
        <div
          className="bj-quick-stakes"
          role="group"
          aria-label={t('快捷选择投入', 'Quick stake selection')}
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
      {!current && <p role="status">{t('正在刷新投入配置…', 'Refreshing stake configuration…')}</p>}
      {!valid && (
        <p role="status">
          {t('请按当前限额和步长选择投入。', 'Choose a stake within the current limits and step.')}
        </p>
      )}
    </form>
  );
}

export function BlackjackGame() {
  const t = useDuelText();
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
      hit: t('要牌', 'Hit'),
      stand: t('停牌', 'Stand'),
      double: t('加倍', 'Double'),
      split: t('分牌', 'Split'),
    })[action];
  const controls = home && own?.state === 'playing' && (
    <section className="bj-controls" aria-label={t('出牌操作', 'Hand actions')}>
      {Object.entries(actions)
        .filter(([, legal]) => legal.length > 0)
        .map(([hand, legal]) => (
          <div key={hand}>
            <strong>
              {t('手牌', 'Hand')} {Number(hand) + 1}
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
          ? t('操作已受理，等待本秒发牌。', 'Action accepted; waiting for this second’s deal.')
          : Object.values(actions).every((a) => !a.length)
            ? t(
                '你已结束决策，等待同桌玩家。',
                'Your decisions are complete. Waiting for the table.',
              )
            : t(
                '每秒处理一个动作，超时自动停牌。',
                'One action per second. Unfinished hands stand at timeout.',
              )}
      </p>
    </section>
  );
  return (
    <main className="game-page">
      <div ref={surface} className="bidding-game blackjack-game">
        <header className="bid-heading">
          <div>
            <span className="bid-eyebrow">BLACKJACK · ONE TABLE · 30s</span>
            <h1>{t('二十一点', 'Blackjack')}</h1>
            <p>{t('同桌决策，各自与庄家比点。', 'One table. Your own hand against the dealer.')}</p>
          </div>
          <div className="duel-actions">
            <Link className="btn btn-secondary" to="/games">
              {t('游戏中心', 'Game center')}
            </Link>
            <button className="btn btn-secondary" onClick={() => setPanel('rules')}>
              {t('游戏规则', 'Rules')}
            </button>
            <button className="btn btn-secondary" onClick={() => setPanel('history')}>
              {t('对局记录', 'History')}
            </button>
            <ArcadeAudioControls sound={sound} unavailable={audio.unavailable} />
          </div>
        </header>
        {snapshot.data && <GameWallets wallets={snapshot.data} />}
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
            <section className="bj-timing" aria-label={t('牌局阶段', 'Round phase')}>
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
                      [t('落座', 'Seating'), t('共同决策', 'Decisions'), t('展示结果', 'Results')][
                        i
                      ]
                    }
                    <small>{[5, 20, 5][i]}s</small>
                  </li>
                ))}
              </ol>
              <strong
                className={`bid-clock ${remaining !== null && remaining <= 5 ? 'is-urgent' : ''}`}
                role="timer"
                aria-label={t('本阶段剩余秒数', 'Seconds remaining')}
              >
                {remaining ?? '—'}
                <small>s</small>
              </strong>
            </section>
            <p className="bid-hint">
              {t('下一局', 'Next round')} {new Date(home.next_round_at * 1000).toLocaleTimeString()}{' '}
              · {t('候补', 'Waiting')} {home.queue_count}
            </p>
            {home.table ? (
              <BlackjackBoard table={home.table} ownSeat={home.your_seat} controls={controls} />
            ) : (
              <div className="bj-idle">
                <span aria-hidden="true">A ♠</span>
                <h2>{t('牌桌等待入席', 'The table is waiting')}</h2>
                <p>
                  {t(
                    '一人即可开局，最多九席。其余玩家旁观并保留队列。',
                    'One player is enough; nine seats maximum. Others watch and keep their queue position.',
                  )}
                </p>
              </div>
            )}
            {home.table?.phase === 'result' && (
              <BlackjackSettlement fact={home.table.fact} seat={home.your_seat} />
            )}
            {home.table?.phase === 'cancelled' && (
              <section className="bj-settlement" data-settlement>
                <h2>{t('本局已取消', 'Table cancelled')}</h2>
                <p>
                  {t(
                    '全部投入按原积分组成无抽水退款；未落座的候补仍保留排位。',
                    'Every stake was refunded in its original assets without fees. Unseated waiters keep their position.',
                  )}
                </p>
              </section>
            )}
            <section className="bj-queue" aria-label={t('入队与落座', 'Queue and seating')}>
              {own ? (
                <>
                  <h2>
                    {own.state === 'waiting'
                      ? `${t('候补排位', 'Queue position')} #${own.position}`
                      : own.state === 'seated'
                        ? `${t('已落座', 'Seated')} #${(own.seat ?? 0) + 1}`
                        : t('本局进行中', 'Playing this round')}
                  </h2>
                  <p>
                    {t('基础投入', 'Base stake')} {formatCredits(own.stake)} ·{' '}
                    {t('已付游戏积分', 'Game credits held')} {formatCredits(own.payment.game)} ·{' '}
                    {t('通用积分', 'General credits')} {formatCredits(own.payment.general)}
                  </p>
                  <p>
                    {t('冻结费用', 'Frozen fees')} {t('平台', 'Platform')}{' '}
                    {own.rake_bp.platform / 100}% · {t('低保', 'Welfare')}{' '}
                    {own.rake_bp.welfare / 100}% · {t('周四', 'Thursday')}{' '}
                    {own.rake_bp.thursday / 100}%
                  </p>
                  {own.state !== 'playing' && (
                    <button
                      type="button"
                      className="btn btn-secondary"
                      disabled={game.blocked || (own.state === 'seated' && remaining === 0)}
                      onClick={() => game.run({ kind: 'leave', id: own.id })}
                    >
                      {t('退出并原币退款', 'Leave and refund original credits')}
                    </button>
                  )}
                  <p className="bid-hint">
                    {own.state === 'waiting'
                      ? t(
                          '候补持续有效，空位按顺序递补。下一局可能自动落座；可随时退出。',
                          'Your place remains valid across rounds. You may be seated next round automatically; leave at any time.',
                        )
                      : t(
                          '本局结束后须主动重新排队，不会自动续投。',
                          'Join the queue again after this table settles. No automatic re-entry.',
                        )}
                  </p>
                </>
              ) : (
                <>
                  <h2>{t('下一手，由你决定', 'Your next hand is your choice')}</h2>
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
                    {t('步长', 'Step')} {formatCredits(home.config.stake_step)} ·{' '}
                    {t('每手返还费用合计', 'Total fees on each return')}{' '}
                    {(home.config.rake_bp.platform +
                      home.config.rake_bp.welfare +
                      home.config.rake_bp.thursday) /
                      100}
                    %
                  </p>
                  <p className="bid-hint">
                    {t(
                      '入队即预留投入，游戏积分优先。每次只参加一局；候补可随时退款退出。',
                      'Joining reserves your stake, game credits first. Each entry plays once. Waiting players may leave for a refund at any time.',
                    )}
                  </p>
                </>
              )}
            </section>
            {home.your_seat !== null && home.table && home.phase !== 'cancelled' && (
              <div className="bj-emotes" role="group" aria-label={t('预设表情', 'Preset emotes')}>
                {BLACKJACK_EMOTES.map((emote) => (
                  <button
                    className="btn btn-secondary"
                    key={emote}
                    disabled={game.blocked}
                    onClick={() => {
                      if (home.table) game.run({ kind: 'emote', id: home.table.id, emote });
                    }}
                  >
                    {emoteText(emote, t)}
                  </button>
                ))}
              </div>
            )}
          </>
        )}
        {!!game.error && <ErrorState error={game.error} />}
        {game.uncertain && (
          <button className="btn btn-primary" disabled={game.pending} onClick={game.retry}>
            {t('确认上次操作结果', 'Confirm previous action')}
          </button>
        )}
        {panel === 'rules' && <Rules close={close} />}
        {panel === 'history' && <History close={close} />}
        <LeaderboardTabs
          items={[
            {
              id: 'net-profit',
              label: t('赌神榜', 'Card master leaderboard'),
              content: <Leaderboard board="blackjack_net_profit" />,
            },
            {
              id: 'profit',
              label: t('利润榜', 'Profit leaderboard'),
              content: <Leaderboard board="blackjack" />,
            },
          ]}
        />
      </div>
    </main>
  );
}
