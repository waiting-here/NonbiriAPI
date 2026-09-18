export function duelConfigFixture(game: 'bidding' | 'likes') {
  return { enabled: false, modes: Object.fromEntries(
    (game === 'bidding' ? ['tier1', 'tier2', 'tier3'] : ['quick', 'standard']).map((key) => [key,
      { enabled: false, ticket: '1', rake_bp: { platform: 0, welfare: 0, thursday: 0 } },
    ]),
  ) };
}
