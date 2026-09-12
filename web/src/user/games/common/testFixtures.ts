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

export function onboardingWire() {
  return {
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
