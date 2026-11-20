export type WhaleExpression = 'cheerful' | 'calm' | 'pout' | 'panicked' | 'teary';

export type WhaleOutfitId =
  'maid_classic' | 'winter_snow' | 'knit_beret' | 'whale_hoodie' | 'sailor_breeze' | 'coral_pajama';

export type WhaleRenderMode = 'pet' | 'rig' | 'sprite';

export type FeedItemId = 'silver_fish' | 'sweet_shrimp' | 'berry_teacake';

export type DecorItemId =
  'jelly_lamp' | 'coral_tea' | 'golden_clam' | 'shell_cabinet' | 'pearl_clock' | 'sea_planter';

export type CatchActionKind = 'gentle' | 'flick' | 'treat' | 'hire';

export interface FeedCatalogItem {
  readonly id: FeedItemId;
  readonly name: string;
  readonly subtitle: string;
  readonly priceGameMilli: number; // 仅限游戏积分购入 (1 credit = 1000 millicredits)
  readonly grams: number; // 0 for instant snack
  readonly basePearlsPerServing: number; // 100% 锚定值（满180g在92%白板下产 0.92×base，满级120%下产 1.20×base）
  readonly digestSecondsPerServing: number; // 单碗 180g 标准消化耗时（秒）
  readonly pearlMultiplier: number; // legacy compat display factor
  readonly instantSatiety?: number;
  readonly moodBonus: number;
  readonly affectionBonus: number;
  readonly badge: string;
}

export interface DecorCatalogItem {
  readonly id: DecorItemId;
  readonly name: string;
  readonly artwork: string;
  readonly description: string;
  readonly pearlCost: number;
  readonly perkText: string;
  readonly conversionBonusPct?: number; // 装备时提供的干饭转化率增益百分比
}

export interface PresetMessage {
  readonly id: string;
  readonly category: 'gentle' | 'flick' | 'treat' | 'hire' | 'visit';
  readonly text: string;
}

export interface WalletState {
  pendingSocialPearls?: number;
  pendingConchShards?: number;
  wageProgress?: number;
  generalMilli: string | number; // 悠哉通用积分 (1.000 = 1000，唯一兑出目标，达 300k 触发熔断)
  gameMilli: string | number; // 游戏积分 (1.000 = 1000，唯一购粮入口，防复利循环)
  pearls: number; // 小鲸币 (1 枚基准面值 = 1,000 通用积分毛额)
  conchShards: number; // 🐚 虹彩海螺碎片 (蹭饭/招待溢出掉落，5片抽1次小筑内循环扭蛋)
  siteDayKey: string; // 当前站点自然日键 (YYYY-MM-DD，用于跨日重置兑换/社交配额)
  dailyPearlExchanged: number; // 今日已兑换成通用积分的小鲸币数 (上限 75 枚/日)
  dailySocialPearlsGained: number; // 今日通过串门/抓包/招待获得的社交小鲸币 (上限 5 枚/日)
  dailyConchShardsGained: number; // 今日已拾取的虹彩海螺碎片数 (上限 10 片/日)
  dailyFreeFeedClaimed?: boolean; // 已废弃：网关正式版不发放免费口粮
}

export type WhaleLocation =
  | { readonly kind: 'home' }
  | {
      readonly kind: 'visiting';
      readonly neighborId: string;
      readonly startedAt: number;
      readonly eatenGrams: number;
      readonly pendingPearls: number;
      readonly pendingConchShards?: number;
    };

export interface TempEmotion {
  readonly expression: WhaleExpression;
  readonly line: string;
  readonly expiresAt: number;
}

export interface WhaleState {
  name: string;
  hasCompletedNaming?: boolean; // 首次进入领养取名标记（默认名：小鲸鱼）
  satiety: number; // 0..100
  mood: number; // 0..100
  affection: number; // 累计羁绊值
  outfit: WhaleOutfitId;
  pinnedExpression?: string | null; // 试衣间手动锁定表情（null 为随游戏状态自动切换）
  pinnedPetState?: string | null; // Codex 桌宠模式手动锁定动作行（null 为随游戏状态自动切换）
  petRoamEnabled?: boolean; // 桌宠海底自由散步开关
  petLookEnabled?: boolean; // 桌宠鼠标视线跟随开关
  isSleeping?: boolean; // 趴在蓝鲸软垫小窝呼呼大睡状态（省粮回心情，睡满30分钟醒来获4h「元气+5%转化率」Buff，不再凭空产币）
  sleepingSince?: number;
  energizedUntil?: number; // 睡醒后获得的「午睡元气 +5% 转化率」Buff 截止时间戳
  petModeMigrated?: boolean;
  renderMode?: WhaleRenderMode;
  location: WhaleLocation;
  tempEmotion: TempEmotion | null;
}

export interface FedBucketEntry {
  readonly hourSlot: number; // Math.floor(timestamp / 3_600_000)
  readonly grams: number;
}

export interface SanctuaryBuffsState {
  autoRefillTickets: number; // 🍲 定时续碗保温券张数
  autoRefillEnabled: boolean; // 是否开启空碗自动从背包续米饭
  aromaCharges: number; // ✨ 五常新米增香包库存
  aromaActiveGrams: number; // 当前饭碗中享受 +35% 消化提速的剩余克数
  wardrobeDiscountCoupons: number; // 🎀 衣橱/家具 8 折海螺抵用券
}

export interface HomeSanctuaryState {
  troughGrams: number; // 0..360
  troughFeedType: 'silver_fish' | 'sweet_shrimp';
  unclaimedPearls: number;
  pearlProgress: number; // 0..100
  accruedWorkerWage?: number; // 打工客鲸帮忙干饭累计待结的工资分成（收币时扣除结算）
  recentFedHistory?: FedBucketEntry[]; // 近 48 小时自家投喂消耗记录（累计满 180g 才解锁社交蹭饭鲸币掉落）
  buffs?: SanctuaryBuffsState; // 海螺扭蛋产出的内循环增益道具
  ownedDecor: DecorItemId[];
  equippedDecor: DecorItemId[];
  ownedOutfits?: WhaleOutfitId[];
}

export interface GuestWhaleVisit {
  slotIndex?: 0 | 1;
  neighborId: string;
  arrivedAt: number;
  eatenGrams: number;
  stage: 'eating' | 'working' | 'panicked' | 'leaving_pout' | 'leaving_teary' | 'treated_happy';
  hiredAt?: number;
  workExpiresAt?: number;
  helpedGrams?: number;
  stageExpiresAt?: number;
  bubbleLine?: string;
}

export interface NeighborStation {
  id: string;
  uid?: number; // NonbiriAPI 站内用户数字 ID (UID)
  discordUsername?: string; // Discord 账号名 (如 guru_captain；仅当 gameProfilePublic=true 时对外可见与可搜)
  discordId?: string; // Discord Snowflake ID (如 384920192837465088；仅公开模式可搜)
  publicNickname: string;
  anonymousCode: string; // 海湾门牌号 (如 4082；匿名与公开模式均可搜，便于私下把门牌发给熟人串门)
  gameProfilePublic: boolean; // NonbiriAPI game_profile_public privacy flag
  whaleName: string;
  whaleOutfit?: WhaleOutfitId;
  oceanTitle: string;
  troughGrams: number;
  troughFeedType: 'silver_fish' | 'sweet_shrimp';
  whaleSatiety: number;
  whaleAtHome: boolean;
  equippedDecor: DecorItemId[];
}

export type NeighborFilterKind = 'all' | 'pinned' | 'has_food' | 'visited';

export interface FootprintRecord {
  neighborId: string;
  theirVisitsCount: number;
  ourVisitsCount: number;
  lastInteractionAt: number;
  lastSummary: string;
  lastPresetMessage?: string;
}

export interface ActivityLogItem {
  id: string;
  timestamp: number;
  kind: 'feed' | 'harvest' | 'visit_out' | 'guest_in' | 'catch' | 'exchange' | 'interact' | 'gacha';
  title: string;
  detail: string;
  presetMessage?: string;
  highlight?: string;
}

export interface GameSnapshot {
  revision: number;
  lastTickAt: number;
  playerUid?: number; // 玩家自身的 NonbiriAPI 站内 UID
  playerDiscordUsername?: string; // 玩家绑定的 Discord 用户名
  playerDiscordId?: string; // 玩家绑定的 Discord 数字 ID
  playerNickname: string;
  playerAnonymousCode: string;
  gameProfilePublic: boolean;
  pinnedNeighborIds?: string[]; // 星标常访好友海湾 ID 列表
  wallet: WalletState;
  inventory: Record<FeedItemId, number>;
  whale: WhaleState;
  home: HomeSanctuaryState;
  guestVisit: GuestWhaleVisit | null;
  guestVisits?: GuestWhaleVisit[];
  neighbors: NeighborStation[];
  footprints: Record<string, FootprintRecord>;
  logs: ActivityLogItem[];
}
