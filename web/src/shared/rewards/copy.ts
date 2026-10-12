import type { Asset, Condition } from './api';
export function assetName(assets: Asset[], id: string, english: boolean) {
  const a = assets.find((a) => a.id === id);
  return a
    ? english
      ? a.name_en
      : a.name
    : id.startsWith('gacha:card:')
      ? english
        ? 'Card'
        : '卡牌'
      : english
        ? 'Reward'
        : '奖励';
}
export function conditionSummary(
  c: Condition,
  assets: Asset[],
  english: boolean,
  t: (key: string) => string,
  time: (value: unknown) => string,
): string {
  if (c.op === 'all') return t('common.rewards.logicOptions.all');
  if (c.op === 'compare')
    return [
      t(fieldLabels[c.field ?? 'level']),
      c.asset ? assetName(assets, c.asset, english) : '',
      t(comparisonLabels[c.compare ?? 'gte']),
      c.field === 'banned'
        ? t(rewardLabels[c.value === 'true' ? 'banned' : 'notBanned'])
        : c.field === 'registered_at' && c.value
          ? time(Number(c.value))
          : c.value,
    ]
      .filter(Boolean)
      .join(' ');
  const parts = (c.children ?? []).map((child) =>
    conditionSummary(child, assets, english, t, time),
  );
  return c.op === 'not'
    ? t('common.rewards.logicOptions.not') + ' (' + parts[0] + ')'
    : '(' + parts.join(' ' + t(logicLabels[c.op]) + ' ') + ')';
}

export const rewardLabels: Record<string, string> = {
  code: 'common.rewards.code',
  broadcast: 'common.rewards.broadcast',
  enabled: 'common.rewards.enabled',
  disabled: 'common.rewards.disabled',
  enable: 'common.rewards.enable',
  disable: 'common.rewards.disable',
  send: 'common.rewards.send',
  createCode: 'common.rewards.createCode',
  banned: 'common.rewards.banned',
  notBanned: 'common.rewards.notBanned',
};

export const logicLabels: Record<string, string> = {
  all: 'common.rewards.logicOptions.all',
  and: 'common.rewards.logicOptions.and',
  or: 'common.rewards.logicOptions.or',
  not: 'common.rewards.logicOptions.not',
  compare: 'common.rewards.logicOptions.compare',
};

export const fieldLabels: Record<string, string> = {
  level: 'common.rewards.fields.level',
  donation: 'common.rewards.fields.donation',
  registered_at: 'common.rewards.fields.registered_at',
  registered_days: 'common.rewards.fields.registered_days',
  banned: 'common.rewards.fields.banned',
  asset: 'common.rewards.fields.asset',
};

export const comparisonLabels: Record<string, string> = {
  gte: 'common.rewards.comparisons.gte',
  gt: 'common.rewards.comparisons.gt',
  eq: 'common.rewards.comparisons.eq',
  ne: 'common.rewards.comparisons.ne',
  lte: 'common.rewards.comparisons.lte',
  lt: 'common.rewards.comparisons.lt',
};

export const rejectionLabels: Record<string, string> = {
  unavailable: 'common.rewards.rejections.unavailable',
  conditions: 'common.rewards.rejections.conditions',
  capacity: 'common.rewards.rejections.capacity',
  delivery: 'common.rewards.rejections.delivery',
};
