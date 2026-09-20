import { useQuery } from '@tanstack/react-query';
import { useGameVisibility } from './visibility';
import { blackjackSnapshot } from '@shared/games/blackjack';
import { gameRequest } from './request';
import { configValue } from './duel/normalize';
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
  type OnboardingProgress,
  type OnboardingTaskKey,
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

function normalizeOnboarding(
  value: unknown,
  tasks: readonly OnboardingTaskKey[],
  rewards: readonly string[],
  field: string,
): OnboardingProgress {
  const record = exactRecord(value, ['items', 'all_completed'], [], field);
  if (!Array.isArray(record.items) || record.items.length !== tasks.length) invalidResponse(field);
  const items = record.items.map((value, index) => {
    const item = exactRecord(value, ['key', 'reward', 'asset_type', 'completed'], [], field);
    if (
      item.key !== tasks[index] ||
      item.reward !== rewards[index] ||
      item.asset_type !== 'general'
    )
      invalidResponse(field);
    return {
      key: tasks[index],
      reward: rewards[index],
      assetType: 'general' as const,
      completed: booleanValue(item.completed, field),
    };
  });
  const allCompleted = booleanValue(record.all_completed, field);
  if (allCompleted !== items.every((item) => item.completed)) invalidResponse(field);
  return { items, allCompleted };
}

export function normalizeGamesSnapshot(value: unknown): GamesSnapshot {
  const record = exactRecord(
    value,
    [
      'server_now',
      'balance',
      'game_balance',
      'tutorial_rps_seen',
      'onboarding',
      'games_enabled',
      'fishing',
      'linklink',
      'rps',
      'bidding',
      'likes',
      'blackjack',
    ],
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
  const onboarding = exactRecord(
    record.onboarding,
    ['fishing', 'linklink', 'rps', 'bidding', 'likes', 'blackjack'],
    [],
    'onboarding',
  );
  const gamesEnabled = booleanValue(record.games_enabled, 'games enabled');
  const fishingEnabled = booleanValue(fishing.enabled, 'fishing enabled');
  const linkLinkEnabled = booleanValue(linklink.enabled, 'LinkLink enabled');
  const rpsEnabled = booleanValue(rps.enabled, 'RPS enabled');
  return {
    serverNow: unixTime(record.server_now, 'snapshot server time'),
    balance: creditsValue(record.balance, { signed: true }, 'snapshot balance'),
    gameBalance: creditsValue(record.game_balance, { signed: true }, 'snapshot game balance'),
    tutorialRPSSeen: booleanValue(record.tutorial_rps_seen, 'tutorial flag'),
    onboarding: {
      bidding: normalizeOnboarding(onboarding.bidding, ['complete_tier_1', 'complete_tier_2', 'complete_tier_3', 'first_win'], ['1000', '2000', '5000', '2000'], 'bidding onboarding'),
      likes: normalizeOnboarding(onboarding.likes, ['quick_complete', 'quick_win', 'standard_complete', 'standard_win'], ['1000', '2000', '5000', '10000'], 'likes onboarding'),
      blackjack: normalizeOnboarding(onboarding.blackjack, ['complete', 'first_win', 'first_bust', 'first_21', 'first_natural_21'], ['1000', '2000', '3000', '4000', '5000'], 'blackjack onboarding'),
      fishing: normalizeOnboarding(
        onboarding.fishing,
        BAITS,
        ['1000', '1000', '1000'],
        'fishing onboarding',
      ),
      linklink: normalizeOnboarding(
        onboarding.linklink,
        LINKLINK_SPECS,
        ['1000', '2000', '3000'],
        'LinkLink onboarding',
      ),
      rps: normalizeOnboarding(
        onboarding.rps,
        RPS_MODES,
        ['1000', '2000', '5000'],
        'RPS onboarding',
      ),
    },
    gamesEnabled,
    blackjack: blackjackSnapshot(record.blackjack),
    bidding: configValue(record.bidding, 'bidding', ['tier1', 'tier2', 'tier3']),
    likes: configValue(record.likes, 'likes', ['quick', 'standard']),
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
  const visible = useGameVisibility();
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
    refetchInterval: visible ? 10_000 : false,
    refetchOnWindowFocus: 'always',
    refetchOnReconnect: 'always',
    retry: false,
  });
}
