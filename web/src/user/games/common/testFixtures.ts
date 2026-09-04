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
    tutorial_rps_seen: false,
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
