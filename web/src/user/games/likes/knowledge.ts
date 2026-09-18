import type { Buff, Effect, ModeCatalog, Passive, Skill } from './catalog';
import type { Translate } from './labels';

export type GuideLevel = 'base' | 'I' | 'II';
export interface GuideEntry {
  id: string;
  title: string;
  summary: string;
  paragraphs: string[];
  meme: string;
  refs: string[];
}
const link = (id: string) => `[[${id}]]`;
const references = (text: string) =>
  [...text.matchAll(/\[\[([^\]]+)\]\]/g)].map((match) => match[1]);
export const guideLevels: readonly GuideLevel[] = ['base', 'I', 'II'];
export function levelName(level: GuideLevel, t: Translate) {
  return level === 'base' ? t('原版', 'Original') : `${t('蒸馏', 'Distilled')} ${level}`;
}

export function entryTitle(c: ModeCatalog, id: string, t: Translate): string {
  const entry = [...c.skills, ...c.buffs, ...c.harnesses, ...c.passives].find(
    (item) => item.id === id,
  );
  if (!entry) return t('未找到词条', 'Entry not found');
  if (!c.buffs.some((item) => item.id === id)) return entry.name;
  const suffix = id.split(':')[1];
  const version =
    suffix === '原版'
      ? t('原版', 'Original')
      : suffix === '高阶'
        ? t('蒸馏 II', 'Distilled II')
        : suffix === '低阶'
          ? t('蒸馏 I', 'Distilled I')
          : suffix === '原版/高阶'
            ? t('原版／蒸馏 II', 'Original / Distilled II')
            : '';
  return version ? `${entry.name} · ${version}` : entry.name;
}

function buffText(c: ModeCatalog, b: Buff, t: Translate): string[] {
  const { p, q, n, cap, kind } = b;
  const timed: Record<string, string> = {
    AMPLIFY: t(
      `主技能与预选额外技能的基础赞数加上条件奖励大于 0 时，每次增加 ${p} 赞。`,
      `Each main or preselected extra skill with positive base-plus-conditional likes gains ${p} more likes.`,
    ),
    SUPPRESS: t(
      `主技能与预选额外技能的基础赞数加上条件奖励大于 0 时，每次减少 ${p} 赞；最终不低于 0，不扣除已获得的赞。`,
      `Each main or preselected extra skill with positive base-plus-conditional likes loses ${p} likes, to a minimum of zero. Previously earned likes are unchanged.`,
    ),
    TOKEN_TAX: t(
      `每个主技能和预选额外技能的 Token 费用增加 ${p} K。原本零 Token 的技能仍为零。`,
      `Each main and preselected extra skill costs ${p} K more tokens. Skills with zero base token cost remain free.`,
    ),
    NONBASIC_TAX: t(
      `每个非普攻的主技能和预选额外技能的 Token 费用增加 ${p} K。原本零 Token 的技能仍为零。`,
      `Each non-basic main or preselected extra skill costs ${p} K more tokens. Zero-base-cost skills remain free.`,
    ),
    SAVE_ENERGY: t(
      `每个主技能和预选额外技能节省 ${p} 电能。正基础耗电最终至少为 ${c.parameters.ENERGY_FLOOR}；零基础耗电保持为零。`,
      `Each main and preselected extra skill saves ${p} energy. Positive base costs still cost at least ${c.parameters.ENERGY_FLOOR}; zero base costs remain zero.`,
    ),
    API_DISCOUNT: t(
      `仅 API 技能、手动选择 API 的混合技能，以及 DeepSeek 使用的混合技能，每次减少 ${p} K Token 费用。这是固定减免，不是百分比；自动支付中因订阅不足或封禁而改用 API 不单独触发此减免。正基础 Token 费用最终至少为 ${c.parameters.TOKEN_FLOOR} K。`,
      `API-only skills, mixed skills explicitly set to API, and DeepSeek's mixed skills save ${p} K tokens per cast. This is a fixed reduction, not a percentage. An automatic API fallback from depleted or banned subscriptions does not by itself qualify. Positive base token costs retain a ${c.parameters.TOKEN_FLOOR} K minimum.`,
    ),
    REGULATOR: t(
      `下一轮每个主技能和预选额外技能节省 ${c.parameters.REGULATOR_SAVE} 电能，正基础耗电最低 ${c.parameters.ENERGY_FLOOR}。购买当轮不生效。`,
      `During the next round each main and preselected extra skill saves ${c.parameters.REGULATOR_SAVE} energy, with a ${c.parameters.ENERGY_FLOOR} minimum for positive base costs. It does not apply on the purchase round.`,
    ),
  };
  if (timed[kind])
    return [
      timed[kind],
      t(
        `从下一轮起持续 ${n} 轮，不作用于触发的 Flash 连答。每轮结束消耗一轮持续时间，跳过、过载和施放失败也计时。`,
        `Starts next round and lasts ${n} rounds. Triggered Flash follow-ups are excluded. Every round consumes duration, including skips, overload recovery and failed casts.`,
      ),
      b.reapply === 'add'
        ? t(
            `再次获得时，先结算本轮持续时间，再增加 ${n} 轮，最多保留 ${cap} 轮；强度不叠加。`,
            `Reapplying adds ${n} rounds after this round's duration is consumed, capped at ${cap}; strength does not stack.`,
          )
        : t(
            `再次获得会刷新为 ${n} 轮，强度不叠加。`,
            `Reapplying refreshes the duration to ${n} rounds without stacking strength.`,
          ),
      t(
        '不同词条以及独立的原版／蒸馏效果可以并存。正面效果可驱散，负面效果可净化。',
        'Distinct entries and separately named original/distilled effects coexist. Positive buffs can be dispelled and negative buffs cleansed.',
      ),
    ];
  switch (kind) {
    case 'CACHE': {
      const scope = b.cacheScope ?? '';
      const target = scope.endsWith('flash')
        ? t('Flash 连答', 'Flash')
        : scope.endsWith('pro')
          ? t('Pro 慢答', 'Pro')
          : t(
              '扮演猫娘、Hello, world!、礼貌回答、格式化回复和快速模式',
              'Catgirl roleplay, Hello, world!, Polite reply, Formatted reply and Quick mode',
            );
      return [
        t(
          `每层使${scope.startsWith('distilled') ? '蒸馏施放的' : '原版'}${target}减少 ${p} K Token 费用，最多 ${cap} 层，正基础费用最低 ${c.parameters.TOKEN_FLOOR} K。`,
          `Each layer saves ${p} K tokens on ${scope.startsWith('distilled') ? 'distilled' : 'original'} ${target}, up to ${cap} layers. Positive base costs retain a ${c.parameters.TOKEN_FLOOR} K minimum.`,
        ),
        t(
          `成功获得一层时与已有同类缓存合并，满层时也能续存。普通技能新获得的层数从下一轮可用${scope === 'flash' ? '；触发的 Flash 连答获得的层数立即可用于下一次连答' : ''}。`,
          `New layers merge with this cache; acquiring a layer at the cap still refreshes it. Layers from chosen skills become usable next round${scope === 'flash' ? '; layers earned by triggered Flash are immediately usable by the next follow-up' : ''}.`,
        ),
        t(
          `本轮未获得同类缓存${b.refreshOnCombo ? '且未获得连答进度' : ''}时，普通层在轮末清空；跳过出招或本轮发生过载也会清空普通层。`,
          `Ordinary layers expire at round end if no layer of this cache${b.refreshOnCombo ? ' and no follow-up progress' : ''} was gained. Skipping casting or overloading also clears ordinary layers.`,
        ),
        t(
          `经 ${link('PUB22')} 转为持久的层数不会自然消失，也不会因跳过或过载消失；可被驱散。持久层与普通层共用 ${cap} 层上限，优先保留持久层。原版和蒸馏缓存分别积累。`,
          `Layers made persistent by ${link('PUB22')} survive expiry, skips and overload but can be dispelled. Persistent and ordinary layers share the ${cap}-layer cap, keeping persistent layers first. Original and distilled caches accumulate separately.`,
        ),
      ];
    }
    case 'ENERGY_STACK':
      return [
        t(
          `每层使主技能和预选额外技能少耗 ${p} 电能，最多 ${cap} 层；正基础耗电最低 ${c.parameters.ENERGY_FLOOR}。不作用于触发的 Flash。`,
          `Each layer saves ${p} energy on main and preselected extra skills, up to ${cap} layers, with a ${c.parameters.ENERGY_FLOOR} minimum for positive base costs. Triggered Flash is excluded.`,
        ),
        t(
          '新层数下一轮生效，持续到被驱散；跳过或过载不移除。原版和蒸馏层数分别累加、分别封顶。',
          'New layers start next round and persist until dispelled, including through skips and overload. Original and distilled layers accumulate with separate caps.',
        ),
      ];
    case 'COMBO':
      return [
        t(
          `每累计 ${p} 层就消耗这些层数，尝试施放 ${q} 次原版 ${link('GEM01')}，无需携带该技能。先结算主技能和额外技能付款，再逐次检查连答的资源。`,
          `Every ${p} layers are spent to attempt ${q} original ${link('GEM01')} follow-up, even when it is not equipped. Main and extra skills pay first; each follow-up then checks its resources.`,
        ),
        t(
          '成功连答可继续获得进度与原版 Flash 缓存；资源不足的尝试只消耗进度，不扣费、不产生过载。未满触发要求的进度保留，跳过出招或本轮过载会清空。',
          'Successful follow-ups grant progress and original Flash cache. Insufficient resources consume only the attempted progress, without payment or overload. Remaining progress carries over unless casting is skipped or overload occurs.',
        ),
      ];
    case 'STUN':
      return [
        t(
          `从下一轮起眩晕 ${n} 轮，无法使用技能，但仍可购物。购物净化解除全部眩晕后可正常选招；仍眩晕时需要确认跳过出招。`,
          `Stunned for ${n} round starting next round. Skills are unavailable but shopping is allowed. Cleansing all stuns with a purchase restores casting; otherwise confirm Skip casting.`,
        ),
        t(
          '可以净化；再次施加刷新期限，不叠加强度。自然到期后解除，订阅重置时钟仍推进。',
          'Can be cleansed. Reapplying refreshes its duration without stacking strength. It ends on expiry; subscription reset clocks continue.',
        ),
      ];
    case 'OVERLOAD':
      return [
        t(
          `电能或 Token 不足导致出招失败时触发，下一轮起自动跳过 ${n} 轮的全部购物与技能；在 ${link('B34:状态')} 中触发则跳过 ${c.buffs.find((v) => v.kind === 'SPEED_MODE')?.n ?? 2} 轮。`,
          `Failed casting from insufficient energy or tokens causes overload. From next round it automatically skips all shopping and casting for ${n} round, or ${c.buffs.find((v) => v.kind === 'SPEED_MODE')?.n ?? 2} rounds when triggered in ${link('B34:状态')}.`,
        ),
        t(
          '不能净化或驱散，订阅时钟照常推进。共享电能不足只影响本轮耗电大于零的玩家；两方都处于有效过载时按当前赞数结束对局。触发的 Flash 资源不足不会产生过载。',
          'Cannot be cleansed or dispelled; subscription clocks continue. Shared energy shortages affect only players quoting positive energy. If both players remain overloaded the game ends on current likes. Insufficient resources for a triggered Flash do not cause overload.',
        ),
      ];
    case 'SPEED_MODE':
      return [
        t(
          `基础 Token 与电能费用乘 ${p}，再计算缓存、加价、折扣和费用下限；技能得赞经过全部加减后乘 ${q}。金币和图像费用不变。`,
          `Base token and energy costs are multiplied by ${p} before cache, surcharges, discounts and minimum costs. Likes are multiplied by ${q} after additions and deductions. Gold and image costs are unchanged.`,
        ),
        t(
          `影响主技能、蒸馏、额外技能及触发的 Flash。${link('GPT44')} 在轮末切换，下一轮生效；不能净化或驱散，过载不会关闭。此时触发过载须恢复 ${n} 轮。`,
          `Applies to main skills, distillation, extra skills and triggered Flash. ${link('GPT44')} toggles it at round end for the following round. It cannot be cleansed or dispelled and survives overload, which takes ${n} recovery rounds in this mode.`,
        ),
      ];
    case 'SUBSCRIPTION_BAN':
      return [
        t(
          '下一轮起订阅瞬发、总量与图像额度暂停使用；混合支付改用 API，耗图像技能不能提交。试用额度仍优先抵扣 Token；仅订阅技能只有在试用额度足以支付全部 Token 时才可使用。',
          'From next round, subscription burst, total and image quota cannot be used. Mixed payments use API and image-consuming skills cannot be submitted. Trial quota still pays tokens first; subscription-only skills remain usable only when trial quota covers all tokens.',
        ),
        t(
          '升级、重置和时钟继续，净化后恢复使用现有余额。原版与蒸馏 II 持续到净化；蒸馏 I 只封禁下一轮。再次施加保留较长期限，短期封禁不会缩短已有的永久封禁。',
          'Upgrades, resets and clocks continue. Cleansing restores access to existing balances. Original and distilled II last until cleansed; distilled I lasts only the next round. Reapplication keeps the longer duration and never shortens an indefinite ban.',
        ),
      ];
    case 'SOTA_FANATICISM':
      return [
        t(
          `每层让正基础耗电的技能基础费用增加 ${p} 电能，再计算倍速与节能；零基础耗电不变。最多 ${cap} 层，下一轮生效。`,
          `Each layer adds ${p} base energy to skills with positive base energy costs, before speed and savings. Zero base costs stay zero. Stacks to ${cap}, starting next round.`,
        ),
        t(
          '可净化。本轮未得到新层数时，轮末减少一层；满层时再次获得也能维持。',
          'Can be cleansed. Loses one layer at round end when no new layer was gained; acquisition at the cap still maintains it.',
        ),
      ];
    case 'BASE_SUPPRESS':
      return [
        t(
          `每层令所有技能基础得赞减少 ${p}，包含触发的 Flash，修正后基础最低为 0。最多 ${cap} 层，下一轮生效。`,
          `Each layer reduces base likes by ${p} for every skill, including triggered Flash, to a minimum of zero. Up to ${cap} layers, starting next round.`,
        ),
        t(
          '持续到被净化；原版和蒸馏各自叠层、各自封顶，减赞相加。',
          'Lasts until cleansed. Original and distilled stacks have separate caps and their reductions add together.',
        ),
      ];
    case 'MODEL_DEGRADATION':
      return [
        t(
          `下一轮起，每次成功技能按当前层数每层减少 ${p} 赞，再消耗一层；包含零得赞技能和触发的 Flash。最多 ${cap} 层。`,
          `Starting next round, every successful skill loses ${p} likes per current layer, then consumes one layer. Includes zero-like skills and triggered Flash. Up to ${cap} layers.`,
        ),
        t(
          '不会自然到期，可净化；当轮先消耗或净化旧层数，再合并新获得的层数，溢出不保留。',
          'Does not expire naturally and can be cleansed. Old layers are consumed or cleansed before new layers merge at round end; overflow is discarded.',
        ),
      ];
    default:
      throw new Error(`Unsupported effect description: ${kind}`);
  }
}

function passiveText(p: Passive, t: Translate): string[] {
  switch (p.kind) {
    case 'LIKE_STRENGTH':
      return [
        t(
          `基础 Token、电能和原始基础得赞都大于 0 的技能，基础得赞 +${p.p}；原技能还消耗图像额度时再 +${p.q}。`,
          `Skills with positive base token cost, energy cost and original base likes gain +${p.p} base likes, plus another ${p.q} if the original skill costs image quota.`,
        ),
        t(
          '作为基础值参与减赞和倍速。蒸馏按自身费用及当前模板的基础得赞计算，不继承图像费用。',
          'This bonus is part of the base before reductions and speed. Distillation uses its own costs and its current template likes, without inheriting image costs.',
        ),
      ];
    case 'SOTA_ONLY':
      return [
        t(
          `成功技能每向对手施加一种负面效果，就额外对其施加 ${p.p} 层 ${link(p.buffId!)}，下一轮生效。`,
          `Each distinct negative effect a successful skill applies to the opponent also adds ${p.p} layer of ${link(p.buffId!)}, effective next round.`,
        ),
        t(
          '满层溢出也算施加；额外的狂热不递归触发自身，购物和给自己的效果不触发。',
          'Application at the cap still counts. The extra fanaticism does not trigger itself; shopping and self-applied effects do not qualify.',
        ),
      ];
    case 'PRO_EXPERIENCE':
      return [
        t(
          `每成功施放一次普攻，额外得 ${p.p} 赞，包括触发的 Flash。蒸馏属于特殊技能，不触发。`,
          `Each successful basic attack grants ${p.p} extra like, including triggered Flash. Distillation is a special skill and does not qualify.`,
        ),
        t(
          '额外赞不受加减赞效果影响，受倍速最终倍率影响。',
          'The extra likes bypass additive buffs and debuffs but receive the speed multiplier.',
        ),
      ];
    case 'FREE_TRIAL':
      return [
        t(
          `对手每成功施放一个非普攻技能，获得 ${p.p} K 试用额度，下一轮可用。初始为 0，无容量上限。`,
          `Each successful non-basic skill cast by the opponent grants ${p.p} K trial quota, usable next round. Starts at zero with no cap.`,
        ),
        t(
          '试用额度优先抵扣任何技能的最终 Token 费用，剩余再按原支付方式扣除；不受订阅封禁影响，不启动订阅重置时钟。',
          'Trial quota pays final token costs before the skill’s normal payment sources. It is unaffected by subscription bans and does not start subscription clocks.',
        ),
      ];
    case 'API_SPECIALIST':
      return [
        t(
          `每成功施放一个标为“仅 API”的技能，额外得 ${p.p} 赞；蒸馏也符合。`,
          `Each successful skill labelled API-only grants ${p.p} extra like; distillation qualifies.`,
        ),
        t(
          '混合技能手动或自动改用 API、DeepSeek 的混合技能都不触发。额外赞不受加减赞效果影响，受倍速影响。',
          'Mixed skills do not qualify even when paid manually or automatically with API, including DeepSeek mixed skills. The bonus bypasses additive modifiers but receives the speed multiplier.',
        ),
      ];
    case 'VALUE_SUBSCRIPTION':
      return [
        t(
          `升级订阅少付 ${p.p} 金币，最低 0。升级额度、图像增加量与重置时钟不变。`,
          `Subscription upgrades cost ${p.p} less gold, to a minimum of zero. Upgrade amounts, images and reset clocks are unchanged.`,
        ),
      ];
    case 'STUDENT_DISCOUNT':
      return [
        t(
          `轮初点赞严格落后时，本轮所有原始基础得赞大于 0 的技能基础得赞 +${p.p}。平分和零基础得赞不触发。`,
          `When strictly behind on likes at round start, all skills with positive original base likes gain +${p.p} base likes that round. Ties and zero-base-like skills do not qualify.`,
        ),
        t(
          '本轮比分变化不重新判断，加成参与后续加减赞与倍速计算。',
          'The condition is not reevaluated during the round; the bonus participates in subsequent additive modifiers and speed.',
        ),
      ];
    case 'CODE_COMPLETION':
      return [
        t(
          `所有技能基础 Token 费用减少 ${p.p} K，最低 0，再计算倍速和其他修正。降到零后无需 Token，否则仍受正费用下限约束。`,
          `All skills save ${p.p} K base tokens, to a minimum of zero, before speed and other modifiers. A resulting zero base cost stays free; positive base costs still have the normal minimum.`,
        ),
      ];
    default:
      throw new Error(`Unsupported passive description: ${p.kind}`);
  }
}

function effectText(c: ModeCatalog, s: Skill, level: GuideLevel, t: Translate): string[] {
  const f: Effect = { ...s.effects[level] };
  const nominalToken =
    level === 'base' ? s.token : c.skills.find((item) => item.id === 'PUB41')!.token;
  const b = c.buffs.find((item) => item.id === f.buffId);
  if (
    b &&
    ['AMPLIFY', 'SUPPRESS', 'TOKEN_TAX', 'NONBASIC_TAX', 'SAVE_ENERGY', 'API_DISCOUNT'].includes(
      f.kind,
    )
  ) {
    f.p = b.p;
    f.n = b.n;
  }
  const out: string[] = [];
  if (s.id === 'PUB41')
    return [
      t(
        `先按已学习的旧模板施放，之后学习对手上一轮成功的主技能。整局最多成功学习 ${s.learn} 次，每轮至多一次；无模板时本次基础得赞为 0。`,
        `Cast the previously learned template first, then learn the opponent's successful main skill from the previous round. Up to ${s.learn} successful learning changes per game and one per round. With no template, base likes are zero.`,
      ),
      t(
        '新模板先获得蒸馏 I，再次学习同一模板升为 II；更换模板重新从 I 开始。同一成功施放只可学习一次，已达 II 的同模板不消耗学习次数。额外技能、Flash 连答、蒸馏及不可蒸馏技能不能作为样本。',
        'A new template starts at distilled I; learning it again upgrades to II. Switching templates restarts at I. Each successful cast is sampled only once; the same template at II consumes no learning charge. Extra skills, Flash follow-ups, distillation and uncopyable skills are not samples.',
      ),
      t(
        '所有模板沿用蒸馏的 Token、电能和 API 支付，不继承原技能的金币、图像费用或成功次数限制。费用减免与倍速仍按现有效果计算。学习发生在施放之后，新模板下次才能使用。',
        'Every template uses distillation’s own token and energy costs and API payment, without inheriting original gold, image or use limits. Current discounts and speed still apply. Learning happens after casting, so a new template is usable only on a later cast.',
      ),
    ];
  out.push(t(`基础获得 ${f.likes} 赞。`, `Gain ${f.likes} base likes.`));
  const grant = b ? link(b.id) : '';
  switch (f.kind) {
    case 'SCORE':
      break;
    case 'CACHE':
    case 'CACHE_COMBO':
      if (b && f.n > 0)
        out.push(
          t(
            `成功施放后获得 1 层 ${grant}，从下一轮减少对应技能的 Token 消耗。`,
            `On a successful cast gain one layer of ${grant}, reducing matching token costs from next round.`,
          ),
        );
      if (f.extraBuffId && f.n > 0)
        out.push(
          t(
            `同时获得 1 层 ${link(f.extraBuffId)}。`,
            `Also gain one layer of ${link(f.extraBuffId)}.`,
          ),
        );
      break;
    case 'SVG_DECAY':
      out.push(
        t(
          `每次成功后，该版本下次基础得赞减少 ${f.p}，最低 ${f.q}；原版和蒸馏 II 分别累计，换模板不会重置，蒸馏 I 不计数。SVG 绘画不获得或使用普攻缓存。`,
          `After each success this version's next base likes decrease by ${f.p}, to a minimum of ${f.q}. Original and distilled II track separately and template changes do not reset them; distilled I does not count. SVG drawing neither earns nor uses basic-attack cache.`,
        ),
      );
      break;
    case 'PERSIST_CACHE':
      out.push(
        t(
          '净化和驱散完成后，把自己仍持有的缓存层转为持久缓存；本轮新获得的缓存不包括在内。持久层不会自然过期或因跳过／过载清空，但仍可被驱散，与普通层共用缓存上限。',
          'After cleansing and dispelling, make the cache layers you still hold persistent. Newly gained layers this round are excluded. Persistent layers survive expiry, skips and overload but can be dispelled and share the normal cache cap.',
        ),
      );
      break;
    case 'AMPLIFY':
    case 'SAVE_ENERGY':
    case 'API_DISCOUNT':
      out.push(
        t(
          `自己获得 ${grant}，下轮起持续 ${f.n} 轮。${buffText(c, b!, t)[0]}`,
          `Gain ${grant} for ${f.n} rounds starting next round. ${buffText(c, b!, t)[0]}`,
        ),
      );
      break;
    case 'SUPPRESS':
    case 'TOKEN_TAX':
    case 'NONBASIC_TAX':
      out.push(
        t(
          `对手获得 ${grant}，下轮起持续 ${f.n} 轮。${buffText(c, b!, t)[0]}`,
          `Give the opponent ${grant} for ${f.n} rounds starting next round. ${buffText(c, b!, t)[0]}`,
        ),
      );
      break;
    case 'CLEANSE':
    case 'DISPEL':
    case 'CLEANSE_OR_DISPEL': {
      const target =
        f.kind === 'CLEANSE'
          ? t('自己的负面效果', 'your debuffs')
          : f.kind === 'DISPEL'
            ? t('对手的正面效果', 'the opponent’s buffs')
            : t(
                '自己的负面效果或对手的正面效果（先选择方向）',
                'your debuffs or the opponent’s buffs (choose a side first)',
              );
      out.push(
        t(
          `${f.randomTargets ? '随机移除' : '手动选择并移除'}最多 ${f.p} 个${target}。在双方得赞前统一移除，因此可以避免同轮的减赞效果；费用已经扣除，不返还。无目标也可施放。`,
          `${f.randomTargets ? 'Randomly remove' : 'Choose and remove'} up to ${f.p} of ${target}. Removal happens before either side scores, so same-round score reductions can be avoided. Already paid costs are not refunded. Casting without a target is allowed.`,
        ),
      );
      out.push(
        t(
          '不会移除电能、Token 或图像余额，也不能移除过载和倍速状态。眩晕时无法使用净化技能，须先在购物中解除眩晕。',
          'Does not remove energy, tokens or image balances, and cannot remove overload or speed mode. Cleansing skills cannot be used while stunned; use a cleansing purchase first.',
        ),
      );
      break;
    }
    case 'COMBO':
      out.push(
        t(
          `获得 ${f.p} 层 ${grant}，可在本轮触发连答。`,
          `Gain ${f.p} layers of ${grant}, which may trigger follow-ups this round.`,
        ),
      );
      break;
    case 'INSERT':
      out.push(
        t(
          `本次方案可预选最多 ${Math.min(f.p, c.parameters.INSERT_CAP)} 个额外技能，主技能后依次独立付费、结算；不会递归增加新的技能位。若前一招付款失败，后续招式取消。`,
          `Preselect up to ${Math.min(f.p, c.parameters.INSERT_CAP)} extra skill in this plan. It pays and resolves separately after the main skill, without opening further slots. A failed payment cancels later skills.`,
        ),
      );
      break;
    case 'PREDICT_COUNTER':
      out.push(
        t(
          `若本技能成功且对手同轮选择的主技能不是普攻，使对手主技能得赞减少 ${f.p}，自己额外得 ${f.q} 赞。对手付款失败仍可猜中；对手跳过则不触发。自己的额外奖励受倍速影响，对手扣减在其倍速前计算。`,
          `If this skill succeeds and the opponent selected a non-basic main skill this round, reduce their main skill likes by ${f.p} and gain ${f.q} extra likes. Their payment failure still counts as a hit; skipping does not. Your bonus receives speed and their reduction applies before their speed multiplier.`,
        ),
      );
      break;
    case 'AUDIT':
      out.push(
        t(
          `若${f.auditTarget === 'self' ? '自己' : '对手'}上一轮所选主技能成功且不是普攻，额外获得 ${f.p} 赞；预选额外技能和连答不代替主技能。`,
          `Gain ${f.p} additional likes if ${f.auditTarget === 'self' ? 'your' : 'the opponent’s'} selected main skill last round succeeded and was non-basic. Extra skills and follow-ups do not replace that main skill.`,
        ),
      );
      break;
    case 'DUAL_AUDIT':
      out.push(
        t(
          `自己和对手上一轮分别只要成功施放过至少一个非普攻，就分别额外获得 ${f.p} 和 ${f.q} 赞；预选额外技能也算，两项条件独立且各奖励一次。`,
          `Gain ${f.p} extra likes if you successfully cast any non-basic skill last round, and ${f.q} if the opponent did. Preselected extras count. Conditions are independent and each rewards once.`,
        ),
      );
      break;
    case 'LOW_POWER':
      out.push(
        t(
          `双方购物后、技能扣费前，共享电能不高于 ${f.p} 时，额外获得 ${f.q} 赞。`,
          `Gain ${f.q} extra likes when shared energy after shopping but before skill payment is at most ${f.p}.`,
        ),
      );
      break;
    case 'BURST_DRAIN':
      out.push(
        t(
          `双方技能付款后，对手订阅瞬发减少 ${f.p} K，最低 0，总量不变；不影响已经支付的技能。对 DeepSeek 仍保留基础得赞。`,
          `After skill payment, drain ${f.p} K of the opponent’s subscription burst to a minimum of zero, leaving total quota unchanged. Already paid skills are unaffected. Base likes still apply against DeepSeek.`,
        ),
      );
      break;
    case 'SELF_STUN':
      out.push(
        t(
          `自己从下一轮起获得 ${f.n} 轮 ${grant}。`,
          `Become affected by ${grant} for ${f.n} round starting next round.`,
        ),
      );
      break;
    case 'ENERGY_STACK':
      out.push(
        t(
          `自己获得 ${f.n} 层 ${grant}，下轮起每层少耗 ${b?.p ?? f.p} 电能，上限 ${b?.cap ?? f.q} 层。`,
          `Gain ${f.n} layers of ${grant}; from next round each saves ${b?.p ?? f.p} energy, up to ${b?.cap ?? f.q} layers.`,
        ),
      );
      break;
    case 'APOLOGY':
      out.push(
        t(
          `自己获得 ${f.p} 层、对手获得 ${f.q} 层 ${grant}，下轮起降低基础得赞，持续到被净化。`,
          `Apply ${f.p} layers of ${grant} to yourself and ${f.q} to the opponent, reducing base likes from next round until cleansed.`,
        ),
      );
      break;
    case 'DEGRADE':
      out.push(
        t(
          `对手获得 ${f.p} 层 ${grant}；若轮初对手点赞严格高于自己，再加 ${f.q} 层，下轮起生效。`,
          `Give the opponent ${f.p} layers of ${grant}, plus ${f.q} when they were strictly ahead on likes at round start. Effective next round.`,
        ),
      );
      break;
    case 'RESOURCE_GAIN':
      out.push(
        t(
          `主技能、额外技能及 Flash 全部付款后，自己获得 ${f.p} 金币和 ${f.q} K API 余量，不能弥补本轮先前付款的不足。`,
          `After main, extra and Flash payments, gain ${f.p} gold and ${f.q} K API reserve. These gains cannot rescue earlier failed payments this round.`,
        ),
      );
      break;
    case 'CACHE_CONVERT':
      out.push(
        t(
          `把自己的原版 Pro 缓存（含本轮新获层，按其上限计）全部转为原版 Flash 缓存，持久层仍持久。每转换一层，返还本次施放技能标称 Token 费用的 ${f.p}% 为 API 余量（每层 ${Math.floor((nominalToken * f.p) / 100)} K），获得 ${f.q} 金币和 1 层 ${link('B28:通用')}；超过 Flash 缓存上限的转换仍给奖励。`,
          `Convert all original Pro cache, including this round's gains up to its cap, to original Flash cache while preserving persistent layers. Each converted layer grants ${f.p}% of the casting skill's nominal token cost as API reserve (${Math.floor((nominalToken * f.p) / 100)} K), ${f.q} gold and one layer of ${link('B28:通用')}. Layers exceeding the Flash cache cap still grant these rewards.`,
        ),
      );
      break;
    case 'USAGE_RESET':
      out.push(
        t(
          '双方全部技能付款及瞬发削减后，把双方订阅瞬发、总量与图像额度恢复到当前上限，并重新等待首次消耗再启动重置时钟。不恢复 API、金币或试用额度，不移除封禁，不能挽救当轮付款失败。',
          'After all skill payments and burst drains, refill both players’ subscription burst, total and image quota to current caps. Reset clocks wait for the next consumption to restart. API, gold and trial quota are unchanged. Bans remain and earlier payment failures are not rescued.',
        ),
      );
      break;
    case 'TOGGLE_SPEED':
      out.push(
        t(
          `轮末开启或关闭自己的 ${grant}，下一轮起生效，不改变本轮其他技能；无任何资源费用。`,
          `Toggle your ${grant} at round end for the next round, without affecting other skills this round. Costs no resources.`,
        ),
      );
      break;
    case 'SUBSCRIPTION_BAN':
      out.push(
        t(
          `对手从下一轮起获得 ${grant}，${f.n > 0 ? `持续 ${f.n} 轮` : '持续到被净化'}。`,
          `Give the opponent ${grant} starting next round, ${f.n > 0 ? `lasting ${f.n} round` : 'lasting until cleansed'}.`,
        ),
      );
      break;
    default:
      throw new Error(`Unsupported skill description: ${f.kind}`);
  }
  if (f.combo)
    out.push(
      t(
        `同时获得 ${f.combo} 层 ${link('B28:通用')}。`,
        `Also gain ${f.combo} layers of ${link('B28:通用')}.`,
      ),
    );
  return out;
}

export function knowledge(c: ModeCatalog, id: string, level: GuideLevel, t: Translate): GuideEntry {
  const skill = c.skills.find((s) => s.id === id),
    buff = c.buffs.find((b) => b.id === id),
    harness = c.harnesses.find((h) => h.id === id),
    passive = c.passives.find((p) => p.id === id);
  let paragraphs: string[] = [],
    meme = '';
  if (skill) {
    paragraphs = effectText(c, skill, level, t);
    meme = skill.effects[level].meme ?? skill.meme;
  } else if (buff) {
    paragraphs = buffText(c, buff, t);
    meme = buff.meme ?? '';
  } else if (passive) {
    paragraphs = passiveText(passive, t);
    meme = passive.meme;
  } else if (harness) {
    paragraphs = [
      t(
        `增加 ${harness.activeSlots} 个主动槽，共 ${4 + harness.activeSlots} 槽；固定被动在开局公开，未使用的主动技能对对手隐藏，空槽可保留。`,
        `Adds ${harness.activeSlots} active slots for ${4 + harness.activeSlots} total. Fixed passives are public at the start; unused active skills remain hidden from the opponent. Slots may be left empty.`,
      ),
      ...harness.passives.map(
        (pid) =>
          `${link(pid)}：${
            passiveText(
              c.passives.find((p) => p.id === pid)!,
              t,
            )[0]
          }`,
      ),
    ];
    meme = harness.meme;
  }
  const summary = skill
    ? skillBrief(c, skill, level, t)
    : harness
      ? harness.passives
          .map((pid) =>
            passiveBrief(
              c.passives.find((p) => p.id === pid)!,
              t,
            ),
          )
          .join(' ') || paragraphs[0]
      : passive
        ? passiveBrief(passive, t)
        : buff
          ? buffBrief(buff, t) || paragraphs[0]
          : (paragraphs[0] ?? '');
  return {
    id,
    title: entryTitle(c, id, t),
    summary,
    paragraphs,
    meme,
    refs: [...new Set(paragraphs.flatMap(references))],
  };
}

function passiveBrief(p: Passive, t: Translate): string {
  const briefs: Record<string, string> = {
    LIKE_STRENGTH: t(
      `正耗电、正 Token、正基础得赞的技能：基础 +${p.p} 赞；原版消耗图像再 +${p.q}。`,
      `Skills costing energy and tokens with positive base likes: +${p.p} base likes; original image skills add ${p.q} more.`,
    ),
    SOTA_ONLY: t(
      `每施加一种负面效果，额外施加 ${p.p} 层狂热。`,
      `Each applied debuff adds ${p.p} fanaticism layers.`,
    ),
    PRO_EXPERIENCE: t(
      `普攻成功额外 +${p.p} 赞，Flash 连答也有效。`,
      `Successful basics gain +${p.p} likes, including Flash follow-ups.`,
    ),
    FREE_TRIAL: t(
      `对手非普攻成功，获得 ${p.p} K 试用额度，下轮可用。`,
      `Each successful opponent non-basic grants ${p.p} K trial tokens for next round.`,
    ),
    API_SPECIALIST: t(
      `仅 API 技能成功额外 +${p.p} 赞。`,
      `Successful API-only skills gain +${p.p} likes.`,
    ),
    VALUE_SUBSCRIPTION: t(`升级订阅节省 ${p.p} 金币。`, `Subscription upgrades save ${p.p} gold.`),
    STUDENT_DISCOUNT: t(
      `轮初落后时，正基础得赞的技能基础 +${p.p} 赞。`,
      `When behind at round start, skills with positive base likes gain +${p.p} base likes.`,
    ),
    CODE_COMPLETION: t(
      `技能基础 Token 减少 ${p.p} K，最低 0。`,
      `Base skill tokens −${p.p} K, minimum 0.`,
    ),
  };
  return briefs[p.kind] ?? passiveText(p, t)[0];
}

function buffBrief(b: Buff, t: Translate): string {
  if (b.kind === 'CACHE')
    return t(
      `对应普攻每层省 ${b.p} K Token，最多 ${b.cap} 层。`,
      `Matching basics save ${b.p} K tokens per layer, up to ${b.cap} layers.`,
    );
  if (b.kind === 'OVERLOAD')
    return t(
      '恢复期间自动跳过购物与技能。',
      'Automatically skip shopping and casting while recovering.',
    );
  if (b.kind === 'SPEED_MODE')
    return t(
      `基础耗电和 Token ×${b.p}，得赞 ×${b.q}。`,
      `Base energy and tokens ×${b.p}; likes ×${b.q}.`,
    );
  if (b.kind === 'API_DISCOUNT')
    return t(
      `符合条件的 API 施法省 ${b.p} K Token。`,
      `Qualifying API casts save ${b.p} K tokens.`,
    );
  return '';
}

function skillBrief(c: ModeCatalog, s: Skill, level: GuideLevel, t: Translate): string {
  if (s.id === 'PUB41')
    return t(
      '施放已学模板，再学习对手上一轮成功的主技能。',
      'Cast your learned template, then learn the opponent’s previous successful main skill.',
    );
  const f = s.effects[level],
    b = c.buffs.find((item) => item.id === f.buffId);
  const extra: Record<string, string> = {
    CACHE:
      b && f.n
        ? t(
            `缓存每层省 ${b.p} K Token，最多 ${b.cap} 层。`,
            `Cache saves ${b.p} K tokens per layer, up to ${b.cap}.`,
          )
        : '',
    CACHE_COMBO:
      b && f.n ? t('获得 Flash 缓存及连答进度。', 'Gain Flash cache and follow-up progress.') : '',
    SVG_DECAY: t(
      `每次成功后基础赞 −${f.p}，最低 ${f.q}。`,
      `Each success reduces future base likes by ${f.p}, down to ${f.q}.`,
    ),
    PERSIST_CACHE: t('把已有缓存变为持久缓存。', 'Make existing cache persistent.'),
    AMPLIFY: t(
      `下轮起每次加赞 ${b?.p ?? f.p}，持续 ${b?.n ?? f.n} 轮。`,
      `Gain +${b?.p ?? f.p} likes per eligible cast for ${b?.n ?? f.n} rounds, starting next round.`,
    ),
    SUPPRESS: t(
      `下轮起对手每次减赞 ${b?.p ?? f.p}，持续 ${b?.n ?? f.n} 轮。`,
      `Opponent loses ${b?.p ?? f.p} likes per eligible cast for ${b?.n ?? f.n} rounds, starting next round.`,
    ),
    SAVE_ENERGY: t(
      `下轮起每次省电 ${b?.p ?? f.p}，持续 ${b?.n ?? f.n} 轮。`,
      `Save ${b?.p ?? f.p} energy per chosen skill for ${b?.n ?? f.n} rounds, starting next round.`,
    ),
    TOKEN_TAX: t(
      `下轮起对手每招多付 ${b?.p ?? f.p} K Token。`,
      `Opponent pays ${b?.p ?? f.p} K extra tokens per chosen skill from next round.`,
    ),
    NONBASIC_TAX: t(
      `下轮起对手非普攻多付 ${b?.p ?? f.p} K Token。`,
      `Opponent’s non-basic skills cost ${b?.p ?? f.p} K extra tokens from next round.`,
    ),
    API_DISCOUNT: t(
      `下轮起符合条件的 API 技能省 ${b?.p ?? f.p} K Token。`,
      `Qualifying API skills save ${b?.p ?? f.p} K tokens from next round.`,
    ),
    CLEANSE: t(`净化自己最多 ${f.p} 个减益。`, `Cleanse up to ${f.p} of your debuffs.`),
    DISPEL: t(`驱散对手最多 ${f.p} 个增益。`, `Dispel up to ${f.p} opponent buffs.`),
    CLEANSE_OR_DISPEL: t(
      `选择净化自己或驱散对手，最多 ${f.p} 个效果。`,
      `Cleanse yourself or dispel opponent buffs, up to ${f.p} effect.`,
    ),
    COMBO: t(`获得 ${f.p} 层连答进度。`, `Gain ${f.p} follow-up progress.`),
    INSERT: t(`允许预选 ${f.p} 个额外技能。`, `Preselect ${f.p} extra skill.`),
    PREDICT_COUNTER: t(
      `猜中对手非普攻：自己 +${f.q}，对手主招 −${f.p}。`,
      `Predict a non-basic main: you gain ${f.q}, their main loses ${f.p}.`,
    ),
    AUDIT: t(
      `上一轮${f.auditTarget === 'self' ? '自己' : '对手'}非普攻主招成功，再得 ${f.p} 赞。`,
      `Gain ${f.p} more if ${f.auditTarget === 'self' ? 'your' : 'the opponent’s'} previous main was a successful non-basic skill.`,
    ),
    DUAL_AUDIT: t(
      `上一轮自己／对手非普攻成功，各额外 +${f.p}／+${f.q}。`,
      `Your/opponent’s successful non-basic casts last round add ${f.p}/${f.q} likes respectively.`,
    ),
    LOW_POWER: t(
      `付款前电能 ≤${f.p} 时，再得 ${f.q} 赞。`,
      `Gain ${f.q} more when pre-payment energy is ≤${f.p}.`,
    ),
    BURST_DRAIN: t(`削减对手瞬发 ${f.p} K。`, `Drain ${f.p} K opponent burst.`),
    SELF_STUN: t(
      `下一轮自己眩晕 ${f.n} 轮。`,
      `Stun yourself for ${f.n} round starting next round.`,
    ),
    ENERGY_STACK: t(
      `获得 ${f.n} 层持久节能，每层省 ${b?.p ?? f.p} 电能。`,
      `Gain ${f.n} lasting energy-saving layers, saving ${b?.p ?? f.p} each.`,
    ),
    APOLOGY: t(
      `自己／对手获得 ${f.p}／${f.q} 层基础减赞。`,
      `Apply ${f.p}/${f.q} base-suppression layers to yourself/opponent.`,
    ),
    DEGRADE: t(
      `对手降智 ${f.p} 层；轮初落后再加 ${f.q} 层。`,
      `Apply ${f.p} degradation layers, plus ${f.q} if behind at round start.`,
    ),
    RESOURCE_GAIN: t(
      `付款后获得 ${f.p} 金币和 ${f.q} K API。`,
      `After payment gain ${f.p} gold and ${f.q} K API.`,
    ),
    CACHE_CONVERT: t(
      'Pro 缓存转为 Flash，获得 API、金币及连答进度。',
      'Convert Pro cache to Flash, gaining API, gold and follow-up progress.',
    ),
    USAGE_RESET: t(
      '双方订阅和图像回满；不挽救当轮付款失败。',
      'Refill both subscriptions and images; does not rescue failed payments.',
    ),
    TOGGLE_SPEED: t(
      `下轮切换倍速：基础费用 ×${b?.p}，得赞 ×${b?.q}。`,
      `Toggle speed next round: base costs ×${b?.p}, likes ×${b?.q}.`,
    ),
    SUBSCRIPTION_BAN: t(
      '下轮起封禁对手订阅及图像额度。',
      'Ban opponent subscription and images from next round.',
    ),
  };
  return `${t(`基础 ${f.likes} 赞。`, `${f.likes} base likes.`)} ${extra[f.kind] ?? ''}${f.combo ? t(` 另获 ${f.combo} 层连答进度。`, ` Also gain ${f.combo} follow-up progress.`) : ''}`.trim();
}

export function relatedEntries(c: ModeCatalog, root: GuideEntry, t: Translate): GuideEntry[] {
  const seen = new Set([root.id]),
    queue = [...root.refs],
    result: GuideEntry[] = [];
  while (queue.length) {
    const id = queue.shift()!;
    if (seen.has(id)) continue;
    seen.add(id);
    const entry = knowledge(c, id, 'base', t);
    if (!entry.paragraphs.length) continue;
    result.push(entry);
    queue.push(...entry.refs);
  }
  return result;
}
