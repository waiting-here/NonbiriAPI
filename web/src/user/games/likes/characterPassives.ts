import type { Buff, Role } from './catalog';
import type { Translate } from './labels';

export function characterPassive(role: Role, t: Translate) {
  if (!role.passive) return null;
  const entries: Record<string, { name: string; description: string }> = {
    MULTIMODAL: {
      name: t('最强多模态', 'Multimodal mastery'),
      description: t(
        '订阅包含图像额度，与订阅总量同步补充；不额外重复发放。',
        'Subscriptions include image quota that replenishes with total quota, without an additional allocation.',
      ),
    },
    SOTA_PRESSURE: {
      name: t('SOTA 压制', 'SOTA pressure'),
      description: t(
        '主技能自身基础得赞大于零，且步骤开始时严格领先，基础得赞＋1。额外技能与连答不触发。',
        'A main skill with positive original base likes gains +1 base like when ahead at the start of its step. Extra skills and follow-ups do not qualify.',
      ),
    },
    WORLD_KNOWLEDGE: {
      name: t('全网知识库', 'World knowledge'),
      description: t(
        '普攻基础得赞＋1，包含自动 Flash 连答。蒸馏属于特殊技能，不触发。',
        'Basic attacks gain +1 base like, including automatic Flash follow-ups. Distillation is special and does not qualify.',
      ),
    },
    SECURITY_SHIELD: {
      name: t('网安之盾', 'Security shield'),
      description: t(
        '效果抵抗25%；步骤开始时得赞落后再加25%，合计50%。',
        '25% effect resistance, plus 25% when behind at the start of a step, for 50% total.',
      ),
    },
    BLUE_FISH: {
      name: t('蓝色大肥鱼', 'Big blue fish'),
      description: t(
        '没有订阅用量。效果命中25%；步骤开始时领先再加25%，合计50%。',
        'No subscription quota. 25% effect hit, plus 25% when ahead at the start of a step, for 50% total.',
      ),
    },
  };
  return entries[role.passive.id];
}

export function effectCategory(buff: Buff, t: Translate) {
  const category =
    buff.category ??
    ([
      'STUN',
      'STOP',
      'SUBSCRIPTION_BAN',
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
    ? t('状态', 'State')
    : category === 'debuff'
      ? t('减益', 'Debuff')
      : t('增益', 'Buff');
}
