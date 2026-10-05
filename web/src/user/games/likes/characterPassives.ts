import type { Buff, Role } from './catalog';
import type { Translate } from './labels';
export function characterPassive(role: Role, text: Translate) {
  if (!role.passive) return null;
  const entries: Record<
    string,
    {
      name: string;
      description: string;
    }
  > = {
    MULTIMODAL: {
      name: text('likes.multimodalMastery'),
      description: text('likes.subscriptionsIncludeImageQuotaThatReplenishesWith'),
    },
    SOTA_PRESSURE: {
      name: text('likes.sOTAPressure'),
      description: text(
        role.pressurePerDebuff
          ? 'likes.debuffPressure'
          : 'likes.aMainSkillWithPositiveOriginalBase',
      ),
    },
    WORLD_KNOWLEDGE: {
      name: text('likes.worldKnowledge'),
      description: text('likes.basicAttacksGain1BaseLikeIncluding'),
    },
    SECURITY_SHIELD: {
      name: text('likes.securityShield'),
      description: text('likes.message25EffectResistancePlus25WhenBehind'),
    },
    BLUE_FISH: {
      name: text('likes.bigBlueFish'),
      description: text('likes.noSubscriptionQuota25EffectHitPlus'),
    },
  };
  return entries[role.passive.id];
}
export function effectCategory(buff: Buff, text: Translate) {
  const category =
    buff.category ??
    ([
      'STUN',
      'STOP',
      'SUBSCRIPTION_BAN',
      'SUBSCRIPTION_SQUEEZE',
      'SUPPRESS',
      'TOKEN_TAX',
      'NONBASIC_TAX',
      'SOTA_FANATICISM',
      'BASE_SUPPRESS',
      'MODEL_DEGRADATION',
    ].includes(buff.kind)
      ? 'debuff'
      : 'buff');
  return category === 'state'
    ? text('likes.state')
    : category === 'debuff'
      ? text('likes.debuff')
      : text('likes.buff');
}
