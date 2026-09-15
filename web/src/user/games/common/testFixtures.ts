export function gamesSnapshotWire() {
  const mode = {
    enabled: true,
    base: '1',
    pumps_bp: { platform: 100, welfare: 50, thursday: 0 },
    queue_seconds: 60,
    gesture_seconds: 10,
    dealer_seconds: 8,
    follower_seconds: 8,
    queue_capacity: 4096,
  };
  return {
    server_now: 1_800_000_000,
    balance: '12345678901234567890.125',
    game_balance: '0',
    tutorial_rps_seen: false,
    onboarding: onboardingWire(),
    games_enabled: true,
    bidding: duelSnapshotWire('bidding'),
    likes: duelSnapshotWire('likes'),
    blackjack: blackjackSnapshotWire(),
    fishing: {
      enabled: true,
      available: true,
      bait_prices: { worm: '1', lure: '2.5', premium: '10' },
    },
    linklink: {
      enabled: true,
      specs: {
        '6x8': { enabled: true, price: '3', seconds: 150 },
        '8x8': { enabled: false, price: '4', seconds: 180 },
        '10x10': { enabled: true, price: '5', seconds: 240 },
      },
    },
    rps: {
      enabled: true,
      modes: { quick: mode, standard: { ...mode, base: '2' }, deathmatch: { ...mode, base: '3' } },
    },
  };
}

export function duelSnapshotWire(game: 'bidding' | 'likes') {
  const mode = { enabled: false, available: true, ticket: '1',
    rake_bp: { platform: 0, welfare: 0, thursday: 0 },
    terms_hash: 'a'.repeat(64), content_hash: 'b'.repeat(64) };
  return { enabled: false, available: true, queue_seconds: 120, queue_capacity: 4096,
    ...(game === 'bidding' ? { joker_seconds: 10, bid_seconds: 20 } : { plan_seconds: 20, settlement_seconds: 5 }),
    modes: Object.fromEntries((game === 'bidding' ? ['tier1', 'tier2', 'tier3'] : ['quick', 'standard']).map((key) => [key, { ...mode }])) };
}

export function onboardingWire() {
  return {
    blackjack: { items: [], all_completed: true },
    bidding: { items: [], all_completed: true },
    likes: { items: [], all_completed: true },
    "fishing": {
      "items": [
        {
          "key": "worm",
          "reward": "1000",
          "asset_type": "general",
          "completed": false
        },
        {
          "key": "lure",
          "reward": "1000",
          "asset_type": "general",
          "completed": false
        },
        {
          "key": "premium",
          "reward": "1000",
          "asset_type": "general",
          "completed": false
        }
      ],
      "all_completed": false
    },
    "linklink": {
      "items": [
        {
          "key": "6x8",
          "reward": "1000",
          "asset_type": "general",
          "completed": false
        },
        {
          "key": "8x8",
          "reward": "2000",
          "asset_type": "general",
          "completed": false
        },
        {
          "key": "10x10",
          "reward": "3000",
          "asset_type": "general",
          "completed": false
        }
      ],
      "all_completed": false
    },
    "rps": {
      "items": [
        {
          "key": "quick",
          "reward": "1000",
          "asset_type": "general",
          "completed": false
        },
        {
          "key": "standard",
          "reward": "2000",
          "asset_type": "general",
          "completed": false
        },
        {
          "key": "deathmatch",
          "reward": "5000",
          "asset_type": "general",
          "completed": false
        }
      ],
      "all_completed": false
    }
  };
}

export function blackjackSnapshotWire() {
  return { enabled: false, available: true, min_stake: '1000', max_stake: '50000', stake_step: '1000', default_stake: '5000', rake_bp: { platform: 100, welfare: 100, thursday: 100 }, config_hash: 'c'.repeat(64), queue_capacity: 4096, seats: 8, seating_seconds: 15, decision_seconds: 30, round_seconds: 60 };
}
