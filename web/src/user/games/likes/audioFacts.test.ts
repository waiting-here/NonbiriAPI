import { describe, expect, it } from 'vitest';
import { homeValue } from '../common/duel/normalize';
import { likesCodec } from './normalize';
import { likesAudioFacts, likesMusicScene, type LikesHome } from './audioFacts';
import wire from './testdata/authority.json';
import timings from './testdata/timelines.json';

function currentWire(summary: unknown = wire.rounds[1].summary) {
  const presentation = likesCodec.presentation!(summary);
  const duration = likesCodec.presentationDuration!(presentation);
  return {
    server_now: 100,
    queue: null,
    current: {
      profiles: [{ kind: 'anonymous' }, { kind: 'public', display_name: 'Card player' }],
      id: 'lik_AAAAAAAAAAAAAAAAAAAAAA',
      game: 'likes',
      mode: 'standard',
      rules_version: 1,
      content_hash: 'a'.repeat(64),
      revision: '1',
      phase_seq: '1',
      phase: 'settlement',
      round: 2,
      deadline: 100 + duration,
      server_now: 100,
      you: 0,
      locked: [false, false],
      ticket: '1',
      rake_bp: { platform: 100, welfare: 100, thursday: 100 },
      own_payment: { general: '0', game: '0' },
      view: structuredClone(wire.initial),
      resolution: {
        round: 2,
        started_at: 100,
        ends_at: 100 + duration,
        summary,
      },
      round_start: null,
    },
    latest_result: null,
  };
}

function currentHome(summary?: unknown) {
  return homeValue(currentWire(summary), likesCodec);
}

function resultHome(outcome: 'win' | 'loss' | 'draw' | 'system_cancelled'): LikesHome {
  const result = {
    id: 'lik_AAAAAAAAAAAAAAAAAAAAAA',
    game: 'likes',
    mode: 'standard',
    terminal_at: 105,
    outcome,
    reason: 'target',
    scores: [0, 0],
    own_payment: { general: '0', game: '0' },
    own_refund: { general: '0', game: '0' },
    prize_general: '0',
    rake: { platform: '0', welfare: '0', thursday: '0' },
    you: 0,
    profiles: [{ kind: 'anonymous' }, { kind: 'anonymous' }],
    view: wire.initial,
    resolution: {
      round: 2,
      started_at: 100,
      ends_at: 105,
      summary: wire.rounds[1].summary,
    },
  };
  return homeValue(
    { server_now: 105, queue: null, current: null, latest_result: result },
    likesCodec,
  );
}

describe('likes authoritative audio facts', () => {
  it('times each follow-up sound to its actual step without collapsing a long sequence', () => {
    const home = currentHome({
      ...wire.scenarios.chain.summary,
      timeline: timings.scenarios.chain.timeline,
    });
    const facts = likesAudioFacts(home);
    const casts = facts.filter((fact) => fact.cue === 'likes_cast');
    expect(casts.map((fact) => fact.at)).toEqual([103500, 105900, 108300, 110700, 113100]);
    expect(new Set(casts.map((fact) => fact.key)).size).toBe(5);
    const original = resultHome('win');
    const result = {
      ...original,
      latestResult: { ...original.latestResult!, resolution: home.current!.resolution },
    };
    expect(likesMusicScene(result, 116)).not.toBe('win');
    expect(likesMusicScene(result, 117)).toBe('win');
  });
  it('maps presentation events to staged facts with stable keys', () => {
    const home = currentHome();
    const facts = likesAudioFacts(home);
    const cues = new Set(facts.map((fact) => fact.cue));
    expect(cues).toEqual(
      new Set([
        'likes_cast',
        'likes_score_burst',
        'likes_pay',
        'likes_charge',
        'likes_subscription_upgrade',
        'likes_buff',
      ]),
    );
    expect(facts.some((fact) => fact.cue === 'likes_pay' && fact.at === 101429)).toBe(true);
    expect(facts.every((fact) => fact.key.startsWith('likes:lik_A'))).toBe(true);
    expect(likesAudioFacts(home)).toEqual(facts);
    expect(
      new Set(likesAudioFacts(currentHome(wire.scenarios.chain.summary)).map((fact) => fact.cue)),
    ).toContain('likes_combo');
  });

  it('plays a buff again when the authoritative frame refreshes its active turn', () => {
    const summary = structuredClone(wire.scenarios.replenish.summary);
    const roundEnd = summary.frames.find((frame) => frame.stage === 'round-end');
    if (!roundEnd) throw new Error('fixture has no round-end frame');
    const effect = roundEnd.players[0].effects.find((item) => item.key === 'B01:原版');
    if (!effect) throw new Error('fixture has no refreshed buff');
    const facts = likesAudioFacts(currentHome(summary));
    expect(
      facts.some(
        (fact) =>
          fact.cue === 'likes_buff' && fact.key.includes(`${effect.key}:${effect.active_from}`),
      ),
    ).toBe(true);
  });

  it('keeps terminal loss as the dedicated stinger and omits cancelled results', () => {
    const loss = likesAudioFacts(resultHome('loss'));
    expect(loss).toContainEqual({
      key: 'likes:lik_AAAAAAAAAAAAAAAAAAAAAA:2:result',
      cue: 'likes_loss_stinger',
      at: 105000,
    });
    expect(loss.some((fact) => fact.cue === 'common_loss')).toBe(false);
    expect(likesAudioFacts(resultHome('system_cancelled'))).toEqual([]);
    expect(likesMusicScene(resultHome('system_cancelled'), 104)).toBe('lobby');
  });

  it('enters danger on the presented own overload beat without reacting to the opponent', () => {
    const current = currentHome(wire.scenarios.single_overload.summary);
    const home: LikesHome = { ...current, current: { ...current.current!, you: 1 } };
    expect(likesMusicScene(home, 101)).toBe('battle');
    expect(likesMusicScene(home, 102)).toBe('danger');
    expect(likesMusicScene(current, 102)).toBe('battle');
  });

  it('prioritizes own danger over speed and only enters result scenes after resolution end', () => {
    const current = currentHome();
    const player = current.current!.view.players[0];
    const speed = {
      key: 'SPEED_MODE',
      kind: 'SPEED_MODE',
      name: 'speed',
      positive: true,
      p: 1,
      q: 1,
      remaining: 0,
      layers: 0,
      buffId: 'SPEED_MODE',
      activeFrom: current.current!.round,
    };
    const speedHome: LikesHome = {
      ...current,
      current: {
        ...current.current!,
        phase: 'plan',
        resolution: null,
        view: {
          ...current.current!.view,
          players: [{ ...player, effects: [speed] }, current.current!.view.players[1]],
        },
      },
    };
    expect(likesMusicScene(speedHome, 100)).toBe('accelerated');
    expect(
      likesMusicScene({
        ...speedHome,
        current: {
          ...speedHome.current!,
          view: {
            ...speedHome.current!.view,
            players: [
              { ...speedHome.current!.view.players[0], overloaded: true },
              speedHome.current!.view.players[1],
            ],
          },
        },
      }),
    ).toBe('danger');
    expect(likesMusicScene(resultHome('win'), 104)).toBe('battle');
    expect(likesMusicScene(resultHome('win'), 105)).toBe('win');
  });
});
