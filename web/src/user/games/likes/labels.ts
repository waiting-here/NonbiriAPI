import type { ModeCatalog, Skill } from './catalog';
import type { Choice, Player, EffectCue } from './types';

export type Translate = (zh: string, en: string) => string;
export function skillName(c: ModeCatalog, id: string) {
  return c.skills.find((s) => s.id === id)?.name ?? id;
}
export function buffName(c: ModeCatalog, id: string) {
  return c.buffs.find((s) => s.id === id)?.name ?? id;
}
type CacheLabel = Pick<EffectCue, 'kind' | 'buff_id' | 'layers' | 'persistent_layers'>;
export function effectName(c: ModeCatalog, effect: CacheLabel, t: Translate) {
  const name = buffName(c, effect.buff_id);
  if (effect.kind !== 'CACHE' || !effect.persistent_layers) return name;
  const title =
    effect.persistent_layers === effect.layers
      ? t('长效缓存', 'Persistent cache')
      : t('缓存', 'Cache');
  const suffix = name.startsWith('短效缓存') ? name.slice('短效缓存'.length) : ' · ' + name;
  return title + suffix;
}
export function effectLayers(
  effect: Pick<CacheLabel, 'kind' | 'layers' | 'persistent_layers'>,
  t: Translate,
) {
  if (effect.kind !== 'CACHE' || effect.persistent_layers === undefined)
    return t('层数', 'Layers') + ': ' + effect.layers;
  const persistent = effect.persistent_layers,
    temporary = effect.layers - persistent;
  return [
    persistent > 0 ? t('持久层', 'Persistent layers') + ': ' + persistent : '',
    temporary > 0 ? t('短效层', 'Short-lived layers') + ': ' + temporary : '',
  ]
    .filter(Boolean)
    .join(' · ');
}
export function kindName(kind: Skill['kind'], t: Translate) {
  return kind === 'basic'
    ? t('普攻', 'Basic')
    : kind === 'normal'
      ? t('常规', 'Normal')
      : kind === 'special'
        ? t('特殊', 'Special')
        : t('大招', 'Ultimate');
}
export function resourceName(key: string, t: Translate): string {
  return (
    {
      gold: t('金币', 'Gold'),
      likes: t('点赞', 'Likes'),
      burst: t('订阅瞬发', 'Subscription burst'),
      sub: t('订阅总量', 'Subscription total'),
      api: t('API余量', 'API reserve'),
      trial: t('试用额度', 'Trial credits'),
      R_IMAGE: t('图像额度', 'Image quota'),
      energy: t('共享电能', 'Shared energy'),
    }[key] ?? key
  );
}
export function shopName(key: string, t: Translate): string {
  return (
    {
      sub: t('升级订阅', 'Upgrade subscription'),
      api: t('API充值', 'Refill API'),
      charge: t('公共充电', 'Charge battery'),
      cleanse: t('净化道具', 'Cleansing item'),
      regulator: t('稳压器', 'Regulator'),
    }[key] ?? key
  );
}
export const STAGES = [
  'reveal',
  'shopping',
  'payment',
  'cleansing',
  'score',
  'aftereffects',
  'round-end',
] as const;
export function stageName(key: string, t: Translate) {
  return (
    {
      reveal: t('方案揭示', 'Plans revealed'),
      shopping: t('购物与充电', 'Shopping & charge'),
      payment: t('付款与过载检查', 'Payment & overload'),
      cleansing: t('净化与Buff', 'Cleansing & buffs'),
      score: t('得赞结算', 'Likes awarded'),
      aftereffects: t('追加效果', 'Follow-up effects'),
      'round-end': t('轮末变化', 'Round end'),
      'round-start': t('轮初补充', 'Round replenishment'),
      before: t('结算前', 'Before settlement'),
      after: t('结算后', 'After settlement'),
    }[key] ?? key
  );
}
export function reasonName(key: string, t: Translate) {
  return (
    {
      'shared-energy': t('共享电能不足', 'Insufficient shared energy'),
      'personal-resources': t('个人资源不足', 'Insufficient personal resources'),
      'previous-failure': t('前序技能失败，后续取消', 'Cancelled after an earlier failure'),
      'no-main': t('跳过出招', 'No skill selected'),
    }[key] ?? key
  );
}
export function chosenEffect(c: ModeCatalog, p: Player, choice: Choice | null) {
  if (!choice) return null;
  const skill = c.skills.find((s) => s.id === choice.skillId);
  if (choice.skillId === 'PUB41' && p.distill?.template && p.distill.level)
    return c.skills.find((s) => s.id === p.distill?.template)?.effects[p.distill.level] ?? null;
  return skill?.effects.base ?? null;
}
