import { blackjackSnapshotWire } from '../common/testFixtures';

export const tableID = 'bjt_AAAAAAAAAAAAAAAAAAAAAA';
export const entryID = 'bjq_AAAAAAAAAAAAAAAAAAAAAA';
export function blackjackWire(
  phase: 'seating' | 'decision' | 'result' = 'decision',
  viewer: number | null = 0,
) {
  const snapshot = blackjackSnapshotWire();
  const config = {
    enabled: true,
    min_stake: snapshot.min_stake,
    max_stake: snapshot.max_stake,
    default_stake: snapshot.default_stake,
    stake_step: snapshot.stake_step,
    rake_bp: snapshot.rake_bp,
    quick_stakes: snapshot.quick_stakes,
  };
  const start = 1_800_000_000;
  const finished = phase === 'result';
  const hands = (seat: number) => [
    {
      cards: [
        { rank: 8, suit: seat % 4 },
        { rank: 8, suit: seat % 4 },
      ],
      revision: '1',
      units: 1,
      total: { value: 16, soft: false },
      natural: false,
      split: false,
      stood: finished,
      ...(finished ? { outcome: 'win' } : {}),
    },
  ];
  const deadline = start + (phase === 'seating' ? 5 : finished ? 30 : 25);
  return {
    server_now: start + (phase === 'seating' ? 2 : finished ? 25 : 5),
    phase,
    deadline,
    next_round_at: start + 30,
    config,
    config_hash: 'c'.repeat(64),
    queue_count: '1',
    your_seat: viewer,
    you:
      viewer === null || finished
        ? null
        : {
            id: entryID,
            position: '0',
            state: phase === 'seating' ? 'seated' : 'playing',
            stake: '5000',
            rake_bp: config.rake_bp,
            payment: { general: '2000', game: '3000' },
            seat: viewer,
            session_id: tableID,
            pending: false,
            legal_actions:
              phase === 'seating'
                ? {}
                : { '0': ['hit', 'stand', 'double', 'split'], '1': [] as string[] },
          },
    table: {
      id: tableID,
      revision: finished ? '3' : '1',
      started_at: start,
      phase,
      deadline,
      next_round_at: start + 30,
      terminal_at: finished ? start + 25 : null,
      ...(finished ? { reason: 'completed' } : {}),
      fact: {
        cards:
          phase === 'seating'
            ? null
            : {
                seats: Array.from({ length: 9 }, (_, number) => ({ number, hands: hands(number) })),
                dealer: finished
                  ? [
                      { rank: 10, suit: 0 },
                      { rank: 6, suit: 0 },
                      { rank: 10, suit: 1 },
                    ]
                  : [{ rank: 10, suit: 0 }],
                dealer_total: { value: finished ? 26 : 10, soft: false },
                hole_hidden: !finished,
                finished,
              },
        seats: Array.from({ length: 9 }, (_, seat) => ({
          seat,
          stake: '5000',
          rake_bp: config.rake_bp,
          stopped: false,
        })),
        settlements: finished
          ? Array.from({ length: 9 }, (_, seat) => ({
              seat,
              hands: [
                {
                  stake_milli: '5000000',
                  gross_milli: '10000000',
                  net_milli: '9700000',
                  platform_milli: '100000',
                  welfare_milli: '100000',
                  thursday_milli: '100000',
                  outcome: 'win',
                },
              ],
            }))
          : [],
      },
    },
  };
}
