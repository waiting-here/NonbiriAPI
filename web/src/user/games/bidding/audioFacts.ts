import type { DuelHome } from '../common/duel/types';
import type { BiddingView, Reward } from './normalize';

export interface AudioFact {
  readonly key: string;
  readonly cue: string;
  readonly at: number;
}

export type BiddingHome = DuelHome<BiddingView, never, never, never>;

function atSeconds(value: number) {
  return Math.round(value * 1000);
}

function rewardKey(reward: Reward) {
  return `${reward.round}:${reward.side}`;
}

function resultFact(home: BiddingHome, previous: BiddingHome, facts: AudioFact[]) {
  const result = home.latestResult;
  const old = previous.latestResult;
  if (!result || result.id === old?.id) return;
  const cue =
    result.outcome === 'win'
      ? 'common_win'
      : result.outcome === 'loss'
        ? 'common_loss'
        : result.outcome === 'draw'
          ? 'common_draw'
          : null;
  if (!cue) return;
  facts.push({
    key: `bidding:${result.id}:result:terminal`,
    cue,
    at: atSeconds(result.terminalAt),
  });
}

function viewTransitionFacts(
  sessionID: string,
  round: number,
  phaseSeq: string,
  current: BiddingView,
  previous: BiddingView,
  at: number,
  facts: AudioFact[],
) {
  if (
    current.played[0].length > previous.played[0].length ||
    current.played[1].length > previous.played[1].length
  )
    facts.push({ key: `bidding:${sessionID}:${round}:reveal`, cue: 'bidding_reveal', at });

  for (const seat of [0, 1] as const)
    if (previous.jokers[seat] && !current.jokers[seat])
      facts.push({
        key: `bidding:${sessionID}:${round}:${phaseSeq}:king:${seat}`,
        cue: 'bidding_king',
        at,
      });

  const previousRewards = new Map(previous.rewards.map((reward) => [rewardKey(reward), reward]));
  for (const reward of current.rewards) {
    const old = previousRewards.get(rewardKey(reward));
    if (!old) {
      if (reward.status === 'pool')
        facts.push({
          key: `bidding:${sessionID}:${reward.round}:${phaseSeq}:pot-add:${reward.side}`,
          cue: 'bidding_pot_add',
          at,
        });
      continue;
    }
    if (old.multiplier < reward.multiplier)
      facts.push({
        key: `bidding:${sessionID}:${reward.round}:${phaseSeq}:king:${reward.side}`,
        cue: 'bidding_king',
        at,
      });
    if (old.status !== 'pool' && reward.status === 'pool')
      facts.push({
        key: `bidding:${sessionID}:${reward.round}:${phaseSeq}:pot-add:${reward.side}`,
        cue: 'bidding_pot_add',
        at,
      });
    if (old.status === 'pool' && reward.status === 'awarded' && reward.owner !== null)
      facts.push({
        key: `bidding:${sessionID}:${reward.round}:${phaseSeq}:collect:${reward.side}`,
        cue: 'bidding_pot_collect',
        at,
      });
  }
}

function transitionFacts(home: BiddingHome, previous: BiddingHome, facts: AudioFact[]) {
  const current = home.current;
  const old = previous.current;
  if (current) {
    const at = atSeconds(current.serverNow);
    if (!old || old.id !== current.id) {
      facts.push({ key: `bidding:${current.id}:match`, cue: 'common_match', at });
      return;
    }
    viewTransitionFacts(
      current.id,
      current.round,
      current.phaseSeq,
      current.view,
      old.view,
      at,
      facts,
    );
    for (const seat of [0, 1] as const)
      if (!old.locked[seat] && current.locked[seat])
        facts.push({
          key: `bidding:${current.id}:${current.round}:${current.phaseSeq}:lock:${seat}`,
          cue: 'common_lock',
          at,
        });
    return;
  }

  const result = home.latestResult;
  if (!result?.view || !old) return;
  if (result.id === old.id)
    viewTransitionFacts(
      result.id,
      old.round,
      'terminal',
      result.view,
      old.view,
      atSeconds(result.terminalAt),
      facts,
    );
}

export function biddingAudioFacts(home: BiddingHome, previous?: BiddingHome): AudioFact[] {
  if (!previous) return [];
  const facts: AudioFact[] = [];
  transitionFacts(home, previous, facts);
  resultFact(home, previous, facts);
  return facts;
}
