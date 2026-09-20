import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useDuelText } from '../common/duel/copy';
import type { DuelHome, Seat } from '../common/duel/types';
import type { BiddingView, Reward } from './normalize';
import { cardLabel, handSuit } from './labels';
import { RewardCard } from './Cards';
import type { EffectCue } from '../common/audio/assets';

type BiddingHome = DuelHome<BiddingView, never, never, never>;
type Snapshot = { readonly id: string; readonly round: number; readonly view: BiddingView };
type Scene = {
  readonly key: string;
  readonly round: number;
  readonly bids: readonly [number, number] | null;
  readonly rewards: readonly Reward[];
  readonly nextRewards: readonly Reward[];
  readonly awardedTo: Seat | null;
  readonly discarded: boolean;
  readonly carried: boolean;
  readonly terminal: boolean;
  readonly poolBefore: number;
  readonly poolAfter: number;
  readonly you: Seat;
};

function snapshotOf(home: BiddingHome): Snapshot | undefined {
  if (home.queue && !home.current) return undefined;
  if (home.current)
    return { id: home.current.id, round: home.current.round, view: home.current.view };
  if (home.latestResult?.view)
    return {
      id: home.latestResult.id,
      round: home.latestResult.view.played[0].length,
      view: home.latestResult.view,
    };
  return undefined;
}
function newRewards(after: Snapshot, before: Snapshot | undefined) {
  const old = new Set((before?.view.rewards ?? []).map((r) => `${r.round}:${r.side}`));
  return after.view.rewards.filter((r) => !old.has(`${r.round}:${r.side}`));
}
function sceneFrom(
  after: Snapshot,
  before: Snapshot | undefined,
  terminal: boolean,
  you: Seat,
  initialDraw: boolean,
): Scene | null {
  const freshRewards = newRewards(after, before);
  const oldPlayed = before?.view.played[0].length ?? 0;
  const played = after.view.played[0].length;
  const completed = played > oldPlayed;
  if (!completed && !freshRewards.length && !initialDraw) return null;
  const round = completed ? played : after.round;
  const nextRewards = completed ? freshRewards.filter((r) => r.round > round) : freshRewards;
  const rewards = after.view.rewards.filter((r) => r.round === round);
  const owners = rewards.map((r) => r.owner).filter((owner): owner is Seat => owner !== null);
  const awardedTo =
    owners.length && owners.every((owner) => owner === owners[0]) ? owners[0] : null;
  const discarded = rewards.some((r) => r.status === 'discarded');
  return {
    key: `${after.id}:${round}:${completed ? 'settlement' : 'draw'}:${terminal ? 'terminal' : 'live'}`,
    round,
    bids: completed ? [after.view.played[0][played - 1], after.view.played[1][played - 1]] : null,
    rewards,
    nextRewards,
    awardedTo,
    discarded,
    carried: completed && awardedTo === null && !discarded,
    terminal,
    poolBefore: before?.view.pool ?? after.view.pool,
    poolAfter: after.view.pool,
    you,
  };
}
function resultText(scene: Scene, t: ReturnType<typeof useDuelText>) {
  if (scene.discarded) return t('末轮平手，奖池弃置', 'Final tie — the pool is discarded');
  if (scene.carried) return t('平手，奖池累积到下一轮', 'Tie — the pool carries to the next round');
  if (scene.awardedTo === null) return t('本轮结算完成', 'Round settled');
  const owner = scene.awardedTo === scene.you ? t('你', 'You') : t('对手', 'Opponent');
  return t(`${owner} 收走整个奖池`, `${owner} collects the whole pool`);
}
function BidCard({
  value,
  seat,
  className,
}: {
  readonly value: number;
  readonly seat: Seat;
  readonly className: string;
}) {
  const t = useDuelText();
  return (
    <span className={`bid-presentation__bid bid-suit--${seat} ${className}`} aria-hidden="true">
      {cardLabel(value)}
      <small>{handSuit(seat, t).symbol}</small>
    </span>
  );
}

export function BiddingPresentation({
  home,
  onCue,
}: {
  readonly home: BiddingHome | undefined;
  readonly onCue?: (cue: EffectCue) => void;
}) {
  const t = useDuelText();
  const reduced =
    typeof window.matchMedia === 'function' &&
    window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  const latestHome = useRef<BiddingHome | undefined>(home);
  const previous = useRef<Snapshot | undefined>(undefined);
  const activeID = useRef<string | undefined>(undefined);
  const hadQueue = useRef(false);
  const initialized = useRef(false);
  const pendingDraw = useRef<Scene | undefined>(undefined);
  const [scene, setScene] = useState<Scene | null>(null);
  const [step, setStep] = useState<'draw' | 'reveal' | 'settle'>('draw');
  const [stepKey, setStepKey] = useState<string | undefined>(undefined);
  useEffect(() => {
    latestHome.current = home;
  }, [home]);

  // This effect only detects adjacent facts. Timers live in the separate scene effect below.
  useEffect(() => {
    if (!home) return;
    const hadSnapshot = initialized.current;
    initialized.current = true;
    const next = snapshotOf(home);
    if (!next) {
      previous.current = undefined;
      activeID.current = undefined;
      pendingDraw.current = undefined;
      hadQueue.current = !!home.queue;
      queueMicrotask(() => setScene(null));
      return;
    }
    if (activeID.current !== next.id) {
      const initialDraw = (hadQueue.current || hadSnapshot) && next.view.played[0].length === 0;
      activeID.current = next.id;
      previous.current = next;
      hadQueue.current = false;
      pendingDraw.current = undefined;
      setScene(
        initialDraw && home.current && document.visibilityState === 'visible'
          ? sceneFrom(next, undefined, false, home.current.you, true)
          : null,
      );
      return;
    }
    const before = previous.current;
    previous.current = next;
    if (!before || document.visibilityState !== 'visible') return;
    const current = home.current;
    const terminal = !current && home.latestResult?.id === next.id;
    const nextScene = sceneFrom(
      next,
      before,
      terminal,
      current?.you ?? home.latestResult?.you ?? 0,
      false,
    );
    if (nextScene) {
      if (scene?.key.includes(':settlement') && nextScene.key.includes(':draw'))
        pendingDraw.current = nextScene;
      else setScene(nextScene);
    }
  }, [home, scene]);

  useEffect(() => {
    if (!scene) return;
    let cancelled = false;
    const start = window.setTimeout(() => {
      if (!cancelled) {
        setStepKey(scene.key);
        setStep(scene.bids ? 'reveal' : 'draw');
        if (!scene.bids) onCue?.('bidding_pot_add');
        else onCue?.('bidding_reveal');
      }
    }, 0);
    const settle = window.setTimeout(() => {
      if (!cancelled && scene.bids) {
        setStep('settle');
        if (scene.awardedTo !== null) onCue?.('bidding_pot_collect');
        else if (!scene.terminal) onCue?.('bidding_pot_add');
      }
    }, 160);
    const nextDraw = window.setTimeout(() => {
      if (!cancelled && scene.bids && scene.nextRewards.length) {
        setStep('draw');
        onCue?.('bidding_pot_add');
      }
    }, 2600);
    const clear = window.setTimeout(
      () => {
        if (!cancelled) setScene(pendingDraw.current ?? null);
        pendingDraw.current = undefined;
      },
      scene.bids && scene.nextRewards.length ? 4100 : scene.bids ? 2600 : 1500,
    );
    return () => {
      cancelled = true;
      window.clearTimeout(start);
      window.clearTimeout(settle);
      window.clearTimeout(nextDraw);
      window.clearTimeout(clear);
    };
  }, [onCue, scene]);

  useEffect(() => {
    const onVisibility = () => {
      const current = latestHome.current;
      if (document.visibilityState !== 'visible') {
        pendingDraw.current = undefined;
        setScene(null);
      }
      previous.current = current ? snapshotOf(current) : undefined;
    };
    document.addEventListener('visibilitychange', onVisibility);
    return () => document.removeEventListener('visibilitychange', onVisibility);
  }, []);

  useLayoutEffect(() => {
    const visibleStep = stepKey === scene?.key ? step : scene?.bids ? 'reveal' : 'draw';
    if (!scene || visibleStep !== 'draw') return;
    const decks = [0, 1].map((side) =>
      document.querySelector<HTMLElement>(`.bid-reward-deck--${side} > summary`),
    );
    const targets = [0, 1].map((side) =>
      document.querySelector<HTMLElement>(
        `.bid-presentation .bid-reward[data-reward-key="${scene.nextRewards.find((reward) => reward.side === side)?.round ?? scene.round}:${side}"]`,
      ),
    );
    const changed: HTMLElement[] = [];
    decks.forEach((source, side) => {
      const target = targets[side];
      if (!source || !target) return;
      const sourceRect = source.getBoundingClientRect();
      const targetRect = target.getBoundingClientRect();
      if (sourceRect.width <= 0 || targetRect.width <= 0) return;
      target.style.setProperty(
        '--draw-x',
        `${sourceRect.left + sourceRect.width / 2 - targetRect.left - targetRect.width / 2}px`,
      );
      target.style.setProperty(
        '--draw-y',
        `${sourceRect.top + sourceRect.height / 2 - targetRect.top - targetRect.height / 2}px`,
      );
      target.style.setProperty('--draw-scale', `${sourceRect.width / targetRect.width}`);
      target.classList.add('is-drawing-from-deck');
      changed.push(target);
    });
    return () => {
      changed.forEach((target) => {
        target.classList.remove('is-drawing-from-deck');
        target.style.removeProperty('--draw-x');
        target.style.removeProperty('--draw-y');
        target.style.removeProperty('--draw-scale');
      });
    };
  }, [scene, step, stepKey]);

  if (!scene)
    return home?.current ? (
      <section className="bid-presentation bid-presentation--idle">
        <p>
          {t(
            '从手牌中暗选一张并锁定，等待双方亮牌。',
            'Choose a bid privately and lock it, then wait for both bids to be revealed.',
          )}
        </p>
      </section>
    ) : null;
  const visibleStep = stepKey === scene.key ? step : scene.bids ? 'reveal' : 'draw';
  const drawRewards =
    scene.bids && visibleStep === 'draw' ? scene.nextRewards : scene.bids ? [] : scene.nextRewards;
  const shownRewards = visibleStep === 'draw' ? drawRewards : scene.rewards;
  const pool = scene.poolBefore;
  const ownerClass = scene.discarded
    ? 'is-discarded'
    : scene.awardedTo === null
      ? 'is-carry'
      : scene.awardedTo === scene.you
        ? 'is-owner-you'
        : 'is-owner-opponent';
  const ownerLabel =
    scene.awardedTo === null
      ? scene.discarded
        ? t('弃置', 'Discarded')
        : t('累积到下一轮', 'Carry to next round')
      : scene.awardedTo === scene.you
        ? t('你', 'You')
        : t('对手', 'Opponent');
  return (
    <section
      className={`bid-presentation bid-presentation--${visibleStep}`}
      data-reduced-motion={reduced}
      aria-live="polite"
      aria-atomic="true"
    >
      <div className={`bid-presentation__stage ${ownerClass}`}>
        <div className="bid-presentation__side is-you">
          <span>{t('你', 'You')}</span>
          {visibleStep !== 'draw' && scene.bids && (
            <BidCard seat={scene.you} value={scene.bids[scene.you]} className="is-left" />
          )}
        </div>
        <div className="bid-presentation__center">
          <div className="bid-presentation__cards">
            {shownRewards
              .slice(0, 2)
              .sort((a, b) => (a.side === scene.you ? -1 : b.side === scene.you ? 1 : 0))
              .map((reward) => (
                <RewardCard key={`${reward.round}:${reward.side}`} card={reward} />
              ))}
          </div>
          {visibleStep !== 'draw' && (
            <strong className="bid-presentation__pot">
              {pool} {t('分奖池', 'pool points')}
            </strong>
          )}
        </div>
        <div className="bid-presentation__side is-opponent">
          <span>{t('对手', 'Opponent')}</span>
          {visibleStep !== 'draw' && scene.bids && (
            <BidCard
              seat={(1 - scene.you) as Seat}
              value={scene.bids[(1 - scene.you) as Seat]}
              className="is-right"
            />
          )}
        </div>
      </div>
      <div className="bid-presentation__copy">
        <strong>
          {visibleStep === 'draw'
            ? t(
                `第 ${scene.bids ? scene.round + 1 : scene.round} 轮：从两侧牌堆抽取奖励`,
                `Round ${scene.bids ? scene.round + 1 : scene.round}: draw rewards from both decks`,
              )
            : visibleStep === 'reveal' && scene.bids
              ? t('双方同步亮牌', 'Both bids revealed together')
              : scene.bids
                ? resultText(scene, t)
                : t('奖励牌已到位，开始暗选', 'Rewards are ready; choose privately')}
        </strong>
        {scene.bids && visibleStep !== 'draw' && (
          <span>
            {t('你', 'You')} {handSuit(scene.you, t).name} {cardLabel(scene.bids[scene.you])} ·{' '}
            {t('对手', 'Opponent')} {handSuit(1 - scene.you, t).name}{' '}
            {cardLabel(scene.bids[(1 - scene.you) as Seat])}
          </span>
        )}
        {visibleStep === 'settle' && scene.bids && (
          <span>
            {ownerLabel} · {pool} {t('分奖池', 'pool points')}
          </span>
        )}
        {visibleStep === 'settle' && scene.rewards.some((r) => r.multiplier === 2) && (
          <small>{t('Joker 倍率 ×2 已计入奖励', 'Joker multiplier ×2 is included')}</small>
        )}
      </div>
    </section>
  );
}
