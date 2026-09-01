export const BAITS = ['worm', 'lure', 'premium'] as const;
export const LINKLINK_SPECS = ['6x8', '8x8', '10x10'] as const;
export const RPS_MODES = ['quick', 'standard', 'deathmatch'] as const;

export type Bait = (typeof BAITS)[number];
export type LinkLinkSpec = (typeof LINKLINK_SPECS)[number];
export type RPSMode = (typeof RPS_MODES)[number];

export interface RPSModeConfig {
  readonly enabled: boolean;
  readonly base: string;
  readonly pumpsBP: {
    readonly platform: number;
    readonly welfare: number;
    readonly thursday: number;
  };
  readonly queueSeconds: number;
  readonly gestureSeconds: number;
  readonly dealerSeconds: number;
  readonly followerSeconds: number;
  readonly queueCapacity: number;
}

export interface GamesSnapshot {
  readonly serverNow: number;
  readonly balance: string;
  readonly tutorialRPSSeen: boolean;
  readonly gamesEnabled: boolean;
  readonly fishing: {
    readonly enabled: boolean;
    readonly available: boolean;
    readonly baitPrices: Readonly<Record<Bait, string>>;
  };
  readonly linklink: {
    readonly enabled: boolean;
    readonly specs: Readonly<
      Record<
        LinkLinkSpec,
        {
          readonly enabled: boolean;
          readonly price: string;
          readonly seconds: 150 | 180 | 240;
        }
      >
    >;
  };
  readonly rps: {
    readonly enabled: boolean;
    readonly modes: Readonly<Record<RPSMode, RPSModeConfig>>;
  };
}
