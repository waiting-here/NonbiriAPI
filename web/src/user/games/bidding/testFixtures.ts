import type { DuelLobbyContext } from '../common/duel/types';

export const biddingID = 'bid_AAAAAAAAAAAAAAAAAAAAAA';
export const biddingHash = 'a'.repeat(64);
export function biddingViewWire() {
  return {
    dealer: 0,
    hand_remaining: [
      Array.from({ length: 13 }, (_, i) => i + 1),
      Array.from({ length: 13 }, (_, i) => i + 1),
    ],
    played: [[] as number[], [] as number[]],
    rewards: [
      { round: 1, side: 0, rank: 7, multiplier: 1, status: 'pool', owner: null as number | null },
      { round: 1, side: 1, rank: 11, multiplier: 1, status: 'pool', owner: null as number | null },
    ],
    joker_available: [true, true],
    pool_points: 18,
    scores: [0, 0],
    own_selected_card: null as number | null,
  };
}
export function biddingHomeWire(phase = 'bid') {
  const now = Math.floor(Date.now() / 1000);
  return {
    server_now: now,
    queue: null,
    current: {
      profiles: [{ kind: 'anonymous' }, { kind: 'public', display_name: 'Card player' }],
      id: biddingID,
      game: 'bidding',
      mode: 'tier1',
      rules_version: 1,
      content_hash: biddingHash,
      revision: '1',
      phase_seq: '1',
      phase,
      round: 1,
      deadline: now + 20,
      server_now: now,
      you: 0,
      locked: [false, false],
      ticket: '5',
      rake_bp: { platform: 100, welfare: 100, thursday: 100 },
      own_payment: { general: '2', game: '3' },
      view: biddingViewWire(),
      resolution: null,
      round_start: null,
    },
    latest_result: null,
  };
}
export const biddingLobby: DuelLobbyContext = {
  accepting: true,
  wallets: { balance: '100', gameBalance: '50' },
  config: {
    enabled: true,
    available: true,
    modes: Object.fromEntries(
      ['tier1', 'tier2', 'tier3'].map((mode, index) => [
        mode,
        {
          enabled: true,
          available: true,
          ticket: ['5', '10', '50'][index],
          rates: { platform: 100, welfare: 100, thursday: 100 },
          termsHash: biddingHash,
          contentHash: biddingHash,
        },
      ]),
    ),
  },
};
