import { decoded, idempotentOptions } from '@shared/operations/api';
import {
  array,
  boolean,
  decimal,
  integer,
  invalidResponse,
  oneOf,
  record,
  string,
} from '@shared/operations/wire';

export const groups = {
  exchange: ['兑换与抽成', 'Exchange and pool cuts'],
  conversion: ['转化率与等级', 'Conversion and levels'],
  feeding: ['口粮与容量', 'Feed and capacity'],
  decor: ['家具', 'Furniture'],
  outfits: ['衣橱', 'Wardrobe'],
  items: ['道具与海螺扭蛋', 'Items and conch draws'],
  care: ['日常照料', 'Daily care'],
  social: ['串门、招待与工资', 'Visits, hosting and wages'],
} as const;

export const units = {
  general_credits: ['通用积分', 'general credits'],
  game_credits: ['游戏积分', 'game credits'],
  percent: ['%', '%'],
  grams: ['克', 'g'],
  seconds: ['秒', 's'],
  pearls: ['小鲸币', 'pearls'],
  points: ['点', 'points'],
  weight: ['权重', 'weight'],
  shards: ['片', 'shards'],
  points_per_second: ['点/秒', 'points/s'],
  grams_per_second: ['克/秒', 'g/s'],
} as const;

export interface WhaleField {
  key: string;
  group: keyof typeof groups;
  unit: keyof typeof units;
  zh: string;
  en: string;
  min: number;
  max: number;
}
export interface WhaleConfig {
  revision: string;
  enabled: boolean;
  exchange_enabled: boolean;
  values: Record<string, number>;
  fields: WhaleField[];
}
export interface WhalePatch {
  expected_revision: string;
  enabled?: boolean;
  exchange_enabled?: boolean;
  values?: Record<string, number>;
}
function normalize(value: unknown): WhaleConfig {
  const body = record(
    value,
    ['revision', 'enabled', 'exchange_enabled', 'values', 'fields'],
    'Whale Haven configuration',
  );
  const keys = new Set<string>();
  const fields = array(body.fields, 'field specifications', 256).map((raw): WhaleField => {
    const field = record(
      raw,
      ['key', 'group', 'unit', 'zh', 'en', 'min', 'max'],
      'field specification',
    );
    const key = string(field.key, 'field key');
    if (!/^[a-z][a-z0-9_]+$/.test(key) || keys.has(key)) invalidResponse('field key');
    keys.add(key);
    const min = integer(field.min, 'field minimum');
    return {
      key,
      group: oneOf(field.group, Object.keys(groups) as (keyof typeof groups)[], 'field group'),
      unit: oneOf(field.unit, Object.keys(units) as (keyof typeof units)[], 'field unit'),
      zh: string(field.zh, 'Chinese field name', { min: 1 }),
      en: string(field.en, 'English field name', { min: 1 }),
      min,
      max: integer(field.max, 'field maximum', min),
    };
  });
  const values = record(body.values, [...keys], 'settings');
  return {
    revision: decimal(body.revision, 'revision', { positive: true }),
    enabled: boolean(body.enabled, 'game switch'),
    exchange_enabled: boolean(body.exchange_enabled, 'exchange switch'),
    fields,
    values: Object.fromEntries(
      fields.map((field) => [
        field.key,
        integer(values[field.key], field.key, field.min, field.max),
      ]),
    ),
  };
}
const path = '/admin/api/games/whale-haven/config';
export const whaleQueryKey = ['admin', 'operations', 'whale-haven-config'] as const;
export const getWhaleConfig = () => decoded(path, normalize);
export const patchWhaleConfig = (input: WhalePatch, key: string, signal?: AbortSignal) =>
  decoded(path, normalize, idempotentOptions(key, { method: 'PATCH', json: input, signal }));

function precision(unit: WhaleField['unit']) {
  if (unit === 'percent') return 2;
  if (
    unit === 'general_credits' ||
    unit === 'game_credits' ||
    unit === 'points_per_second' ||
    unit === 'grams_per_second'
  )
    return 3;
  return 0;
}
export function displayValue(value: number, unit: WhaleField['unit']) {
  const places = precision(unit);
  const digits = String(value).padStart(places + 1, '0');
  if (!places) return digits;
  const fraction = digits.slice(-places).replace(/0+$/, '');
  return digits.slice(0, -places) + (fraction ? '.' + fraction : '');
}

export function readField(input: string, field: WhaleField): number | null {
  const match = /^(\d+)(?:\.(\d+))?$/.exec(input.trim());
  const places = precision(field.unit);
  if (!match || (match[2]?.length ?? 0) > places || match[1].length > 18) return null;
  const stored = Number(match[1] + (match[2] ?? '').padEnd(places, '0'));
  return Number.isSafeInteger(stored) && stored >= field.min && stored <= field.max ? stored : null;
}

export function economyProblem(v: Record<string, number>): readonly [string, string] | null {
  if (v.platform_cut_bp + v.welfare_cut_bp + v.thursday_cut_bp >= 10000)
    return ['三项抽成合计须低于 100%。', 'The three cuts must total less than 100%.'];
  if (v.conversion_min_bp > v.conversion_base_bp || v.conversion_base_bp > v.conversion_max_bp)
    return [
      '基础转化率必须介于最低和最高转化率之间。',
      'Base conversion must be between the floor and ceiling.',
    ];
  if (v.mood_low_threshold > v.mood_high_threshold)
    return [
      '低落阈值不能超过愉悦阈值。',
      'The low mood threshold must be at or below the happy threshold.',
    ];
  if (v.bank_upgraded_pearls < v.bank_base_pearls)
    return [
      '升级后的存钱罐容量须至少等于普通容量。',
      'The upgraded bank must hold at least as many pearls as the basic bank.',
    ];
  if (v.daily_exchange_pearls > 0 && v.daily_exchange_pearls < v.lot_pearls)
    return ['非零日限须至少可兑一手。', 'A positive daily limit must cover at least one lot.'];
  if (
    v.conch_auto_refill_weight +
      v.conch_aroma_boost_weight +
      v.conch_wardrobe_coupon_weight +
      v.conch_affection_candy_weight ===
    0
  )
    return [
      '至少一种扭蛋奖品的权重须大于零。',
      'At least one conch reward must have a positive weight.',
    ];
  return null;
}

export function exchangePreview(v: Record<string, number>) {
  const gross = v.lot_gross_milli;
  const cuts = ['platform_cut_bp', 'welfare_cut_bp', 'thursday_cut_bp'].map((k) =>
    Math.floor((gross * v[k]) / 10000),
  );
  const net = gross - cuts.reduce((total, cut) => total + cut, 0);
  let bestAffection = 0,
    furniture = 0;
  for (const [key, value] of Object.entries(v)) {
    if (/^affection_level_\d+_bp$/.test(key)) bestAffection = Math.max(bestAffection, value);
    if (/^decor_.*_bonus_bp$/.test(key)) furniture += value;
  }
  const grossBP = Math.min(
    v.conversion_max_bp,
    Math.max(
      v.conversion_min_bp,
      v.conversion_base_bp + bestAffection + furniture + v.mood_high_bonus_bp + v.sleep_bonus_bp,
    ),
  );
  // A feed's long-run return includes its real price, yield and the rounded net value of a whole lot.
  const bestReturnPct = Math.max(
    ...['silver_fish', 'sweet_shrimp'].map(
      (id) =>
        (((v['feed_' + id + '_base_pearls'] * grossBP) / 100) * (net / v.lot_pearls)) /
        v['feed_' + id + '_price_milli'],
    ),
  );
  return { gross, cuts, net, grossBP, bestReturnPct };
}
