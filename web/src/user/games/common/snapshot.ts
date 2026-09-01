import { useQuery } from '@tanstack/react-query';
import { gameRequest } from './request';
import {
  booleanValue,
  creditsValue,
  enumValue,
  exactRecord,
  invalidResponse,
  safeInteger,
  unixTime,
} from './strict';
import {
  BAITS,
  LINKLINK_SPECS,
  RPS_MODES,
  type GamesSnapshot,
  type LinkLinkSpec,
  type RPSMode,
  type RPSModeConfig,
} from './types';

export const gameKeys = {
  snapshot: ['user', 'games', 'snapshot', 'beta1'] as const,
};

function normalizeMode(value: unknown, field: string): RPSModeConfig {
  const record = exactRecord(
    value,
    [
      'enabled',
      'base',
      'pumps_bp',
      'queue_seconds',
      'gesture_seconds',
      'dealer_seconds',
      'follower_seconds',
      'queue_capacity',
    ],
    [],
    field,
  );
  const pumps = exactRecord(
    record.pumps_bp,
    ['platform', 'welfare', 'thursday'],
    [],
    `${field} pumps`,
  );
  const platform = safeInteger(pumps.platform, 0, 9_999, `${field} platform cut`);
  const welfare = safeInteger(pumps.welfare, 0, 9_999, `${field} welfare cut`);
  const thursday = safeInteger(pumps.thursday, 0, 9_999, `${field} Thursday cut`);
  if (platform + welfare + thursday >= 10_000) {
    invalidResponse(`${field} pump total`);
  }
  const enabled = booleanValue(record.enabled, `${field} enabled`);
  const base = creditsValue(record.base, {}, `${field} base`);
  if (enabled && base === '0') invalidResponse(`${field} enabled base`);
  return {
    enabled,
    base,
    pumpsBP: { platform, welfare, thursday },
    queueSeconds: safeInteger(record.queue_seconds, 30, 120, `${field} queue seconds`),
    gestureSeconds: safeInteger(record.gesture_seconds, 5, 20, `${field} gesture seconds`),
    dealerSeconds: safeInteger(record.dealer_seconds, 5, 15, `${field} dealer seconds`),
    followerSeconds: safeInteger(record.follower_seconds, 5, 15, `${field} follower seconds`),
    queueCapacity: safeInteger(record.queue_capacity, 4_096, 4_096, `${field} capacity`),
  };
}

export function normalizeGamesSnapshot(value: unknown): GamesSnapshot {
  const record = exactRecord(
    value,
    ['server_now', 'balance', 'tutorial_rps_seen', 'games_enabled', 'fishing', 'linklink', 'rps'],
    [],
    'games snapshot',
  );
  const fishing = exactRecord(
    record.fishing,
    ['enabled', 'available', 'bait_prices'],
    [],
    'fishing module',
  );
  const baits = exactRecord(fishing.bait_prices, BAITS, [], 'bait prices');
  const linklink = exactRecord(record.linklink, ['enabled', 'specs'], [], 'LinkLink module');
  const specs = exactRecord(linklink.specs, LINKLINK_SPECS, [], 'LinkLink specs');
  const normalizedSpecs = {} as Record<
    LinkLinkSpec,
    GamesSnapshot['linklink']['specs'][LinkLinkSpec]
  >;
  const secondsBySpec: Record<LinkLinkSpec, 150 | 180 | 240> = {
    '6x8': 150,
    '8x8': 180,
    '10x10': 240,
  };
  for (const spec of LINKLINK_SPECS) {
    const specRecord = exactRecord(
      specs[spec],
      ['enabled', 'price', 'seconds'],
      [],
      `${spec} spec`,
    );
    const seconds = safeInteger(
      specRecord.seconds,
      secondsBySpec[spec],
      secondsBySpec[spec],
      `${spec} seconds`,
    );
    const enabled = booleanValue(specRecord.enabled, `${spec} enabled`);
    const price = creditsValue(specRecord.price, {}, `${spec} price`);
    if (enabled && price === '0') invalidResponse(`${spec} enabled price`);
    normalizedSpecs[spec] = {
      enabled,
      price,
      seconds: seconds as 150 | 180 | 240,
    };
  }
  const rps = exactRecord(record.rps, ['enabled', 'modes'], [], 'RPS module');
  const modes = exactRecord(rps.modes, RPS_MODES, [], 'RPS modes');
  const normalizedModes = {} as Record<RPSMode, RPSModeConfig>;
  for (const mode of RPS_MODES) {
    enumValue(mode, RPS_MODES, 'RPS mode key');
    normalizedModes[mode] = normalizeMode(modes[mode], `${mode} mode`);
  }
  const gamesEnabled = booleanValue(record.games_enabled, 'games enabled');
  const fishingEnabled = booleanValue(fishing.enabled, 'fishing enabled');
  const linkLinkEnabled = booleanValue(linklink.enabled, 'LinkLink enabled');
  const rpsEnabled = booleanValue(rps.enabled, 'RPS enabled');
  return {
    serverNow: unixTime(record.server_now, 'snapshot server time'),
    balance: creditsValue(record.balance, { signed: true }, 'snapshot balance'),
    tutorialRPSSeen: booleanValue(record.tutorial_rps_seen, 'tutorial flag'),
    gamesEnabled,
    fishing: {
      enabled: fishingEnabled,
      available: booleanValue(fishing.available, 'fishing runtime availability'),
      baitPrices: {
        worm: creditsValue(baits.worm, { positive: true }, 'worm price'),
        lure: creditsValue(baits.lure, { positive: true }, 'lure price'),
        premium: creditsValue(baits.premium, { positive: true }, 'premium price'),
      },
    },
    linklink: {
      enabled: linkLinkEnabled,
      specs: normalizedSpecs,
    },
    rps: { enabled: rpsEnabled, modes: normalizedModes },
  };
}

export function useGamesSnapshot() {
  return useQuery({
    queryKey: gameKeys.snapshot,
    queryFn: async ({ signal }) =>
      normalizeGamesSnapshot(
        (
          await gameRequest<unknown>('/api/games', {
            signal,
            expectedStatuses: [200],
          })
        ).data,
      ),
    staleTime: 10_000,
    retry: false,
  });
}
