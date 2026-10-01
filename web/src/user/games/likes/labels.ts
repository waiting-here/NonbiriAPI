import type { DuelText } from '../common/duel/copy';
import type { ModeCatalog, Skill } from './catalog';
import type { Choice, Player, EffectCue } from './types';
export type Translate = DuelText;
export function skillName(c: ModeCatalog, id: string) {
  return c.skills.find((s) => s.id === id)?.name ?? id;
}
export function buffName(c: ModeCatalog, id: string) {
  return c.buffs.find((s) => s.id === id)?.name ?? id;
}
type CacheLabel = Pick<EffectCue, 'kind' | 'buff_id' | 'layers' | 'persistent_layers'>;
export function effectName(c: ModeCatalog, effect: CacheLabel, text: Translate) {
  const name = buffName(c, effect.buff_id);
  if (effect.kind !== 'CACHE' || !effect.persistent_layers) return name;
  const title =
    effect.persistent_layers === effect.layers
      ? text('likes.persistentCache')
      : text('likes.cache');
  const suffix = name.startsWith('短效缓存') ? name.slice('短效缓存'.length) : ' · ' + name;
  return title + suffix;
}
export function effectLayers(
  effect: Pick<CacheLabel, 'kind' | 'layers' | 'persistent_layers'>,
  text: Translate,
) {
  if (effect.kind !== 'CACHE' || effect.persistent_layers === undefined)
    return text('likes.layers') + ': ' + effect.layers;
  const persistent = effect.persistent_layers,
    temporary = effect.layers - persistent;
  return [
    persistent > 0 ? text('likes.persistentLayers') + ': ' + persistent : '',
    temporary > 0 ? text('likes.shortLivedLayers') + ': ' + temporary : '',
  ]
    .filter(Boolean)
    .join(' · ');
}
export function kindName(kind: Skill['kind'], text: Translate) {
  return kind === 'basic'
    ? text('likes.basic')
    : kind === 'normal'
      ? text('likes.normal')
      : kind === 'special'
        ? text('likes.special')
        : text('likes.ultimate');
}
export function resourceName(key: string, text: Translate): string {
  return (
    {
      gold: text('likes.gold'),
      likes: text('likes.likes'),
      burst: text('likes.subscriptionBurst'),
      sub: text('likes.subscriptionTotal'),
      api: text('likes.aPIReserve'),
      trial: text('likes.trialCredits'),
      R_IMAGE: text('likes.imageQuota'),
      energy: text('likes.sharedEnergy'),
    }[key] ?? key
  );
}
export function shopName(key: string, text: Translate): string {
  return (
    {
      sub: text('likes.upgradeSubscription'),
      api: text('likes.refillAPI'),
      charge: text('likes.chargeBattery'),
      cleanse: text('likes.cleansingItem'),
      regulator: text('likes.regulator'),
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
export function stageName(key: string, text: Translate) {
  return (
    {
      reveal: text('likes.plansRevealed'),
      shopping: text('likes.shoppingCharge'),
      payment: text('likes.paymentOverload'),
      cleansing: text('likes.cleansingBuffs'),
      score: text('likes.likesAwarded'),
      aftereffects: text('likes.followUpEffects'),
      'round-end': text('likes.roundEnd'),
      'round-start': text('likes.roundReplenishment'),
      before: text('likes.beforeSettlement'),
      after: text('likes.afterSettlement'),
    }[key] ?? key
  );
}
export function reasonName(key: string, text: Translate) {
  return (
    {
      'shared-energy': text('likes.insufficientSharedEnergy'),
      'personal-resources': text('likes.insufficientPersonalResources'),
      'previous-failure': text('likes.cancelledAfterAnEarlierFailure'),
      'no-main': text('likes.noSkillSelected'),
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
