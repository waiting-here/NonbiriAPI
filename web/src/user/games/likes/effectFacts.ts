import type { Buff, Effect } from './catalog';
import type { Translate } from './labels';

type Fact = [string, string];
export function effectFacts(f: Effect, t: Translate): Fact[] {
  const value = (zh: string, en: string, n: number, unit = ''): Fact => [t(zh, en), `${n}${unit}`];
  const pairs: Record<string, Fact[]> = {
    CACHE: [
      value('每层缓存省 Token', 'Tokens saved per cache layer', f.p, ' K'),
      value('缓存层数上限', 'Cache layer cap', f.q),
    ],
    CACHE_COMBO: [
      value('每层缓存省 Token', 'Tokens saved per cache layer', f.p, ' K'),
      value('缓存层数上限', 'Cache layer cap', f.q),
    ],
    SVG_DECAY: [
      value('每次施放后基础得赞下降', 'Base likes lost after each cast', f.p),
      value('基础得赞下限', 'Minimum base likes', f.q),
    ],
    AMPLIFY: [
      value('增幅得赞', 'Amplified likes', f.p),
      value('持续轮数', 'Duration in rounds', f.n),
    ],
    SUPPRESS: [
      value('压制得赞', 'Suppressed likes', f.p),
      value('持续轮数', 'Duration in rounds', f.n),
    ],
    SAVE_ENERGY: [
      value('节省电能', 'Energy saved', f.p),
      value('持续轮数', 'Duration in rounds', f.n),
    ],
    TOKEN_TAX: [
      value('追加 Token 费用', 'Extra token cost', f.p, ' K'),
      value('持续轮数', 'Duration in rounds', f.n),
    ],
    NONBASIC_TAX: [
      value('非普攻追加 Token 费用', 'Extra non-basic token cost', f.p, ' K'),
      value('持续轮数', 'Duration in rounds', f.n),
    ],
    API_DISCOUNT: [
      value('API 折扣', 'API discount', f.p, '%'),
      value('持续轮数', 'Duration in rounds', f.n),
    ],
    CLEANSE: [value('最多净化 Buff 数', 'Maximum buffs cleansed', f.p)],
    DISPEL: [value('最多驱散 Buff 数', 'Maximum buffs dispelled', f.p)],
    CLEANSE_OR_DISPEL: [value('最多处理 Buff 数', 'Maximum buffs removed', f.p)],
    COMBO: [value('获得连答进度', 'Follow-up progress gained', f.p)],
    INSERT: [value('允许额外技能数', 'Extra skills allowed', f.p)],
    PREDICT_COUNTER: [
      value('命中时对手主招减赞', 'Opponent main likes removed on hit', f.p),
      value('命中时额外得赞', 'Extra likes on hit', f.q),
    ],
    AUDIT: [value('审计条件满足时额外得赞', 'Extra likes when the audit condition holds', f.p)],
    DUAL_AUDIT: [
      value('自身审计额外得赞', 'Own audit bonus', f.p),
      value('对手审计额外得赞', 'Opponent audit bonus', f.q),
    ],
    LOW_POWER: [
      value('低电量阈值', 'Low-energy threshold', f.p),
      value('低电量额外得赞', 'Low-energy likes bonus', f.q),
    ],
    BURST_DRAIN: [value('削减对手瞬发额度', 'Opponent burst quota drained', f.p, ' K')],
    SELF_STUN: [value('自身眩晕轮数', 'Own stun duration', f.n)],
    ENERGY_STACK: [
      value('获得节能层数', 'Energy-saving layers gained', f.n),
      value('每层节能', 'Energy saved per layer', f.p),
      value('层数上限', 'Layer cap', f.q),
    ],
    APOLOGY: [
      value('自身基础压制层数', 'Own base-suppression layers', f.p),
      value('对手基础压制层数', 'Opponent base-suppression layers', f.q),
    ],
    DEGRADE: [
      value('施加降智层数', 'Degradation layers applied', f.p),
      value('落后时额外层数', 'Extra layers while behind', f.q),
    ],
    RESOURCE_GAIN: [
      value('获得金币', 'Gold gained', f.p),
      value('获得 API 余量', 'API reserve gained', f.q, ' K'),
    ],
    CACHE_CONVERT: [
      value('每层返还基础 Token 费用', 'Base token cost refunded per layer', f.p, '%'),
      value('每层获得金币', 'Gold gained per layer', f.q),
    ],
  };
  const facts = pairs[f.kind] ?? [];
  if (f.combo) facts.push(value('获得连答进度', 'Follow-up progress gained', f.combo));
  return facts;
}
export function buffFacts(b: Buff, t: Translate): Fact[] {
  const pLabel: Record<string, [string, string, string?]> = {
    CACHE: ['每层 Token 减免', 'Token savings per layer', ' K'],
    AMPLIFY: ['每次增幅得赞', 'Likes added per cast'],
    SUPPRESS: ['每次压制得赞', 'Likes removed per cast'],
    TOKEN_TAX: ['追加 Token 费用', 'Extra token cost', ' K'],
    NONBASIC_TAX: ['非普攻追加费用', 'Extra non-basic cost', ' K'],
    SAVE_ENERGY: ['节省电能', 'Energy saved'],
    REGULATOR: ['节省电能', 'Energy saved'],
    API_DISCOUNT: ['API 折扣', 'API discount', '%'],
    ENERGY_STACK: ['每层节能', 'Energy saved per layer'],
    COMBO: ['每次尝试消耗层数', 'Layers spent per attempt'],
    SOTA_FANATICISM: ['每层基础电能加价', 'Base energy added per layer'],
    BASE_SUPPRESS: ['每层基础得赞减少', 'Base likes removed per layer'],
    MODEL_DEGRADATION: ['每层得赞减少', 'Likes removed per layer'],
    SPEED_MODE: ['基础费用倍率', 'Base cost multiplier'],
  };
  const facts: Fact[] = [],
    name = pLabel[b.kind];
  if (name) facts.push([t(name[0], name[1]), `${b.p}${name[2] ?? ''}`]);
  if (b.kind === 'SPEED_MODE') facts.push([t('得赞倍率', 'Likes multiplier'), String(b.q)]);
  if (b.kind === 'COMBO')
    facts.push([t('每次尝试 Flash 数', 'Flash attempts per trigger'), String(b.q)]);
  if (b.cap > 0) facts.push([t('叠加上限', 'Stacking cap'), String(b.cap)]);
  if (b.n > 0) facts.push([t('基础持续轮数', 'Base duration in rounds'), String(b.n)]);
  return facts;
}
