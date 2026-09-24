import type { DuelRound, Seat } from '../common/duel/types';
import { useDuelText } from '../common/duel/copy';
import type { ModeCatalog } from './catalog';
import type { LikesEvent, LikesView, RoundFacts } from './types';
import type { JSONValue } from './value';
import {
  buffName,
  effectName,
  reasonName,
  resourceName,
  shopName,
  skillName,
  stageName,
} from './labels';
import { FrameChanges } from './Arena';
import { PlanSummary } from './PlanEditor';
import { characterPassive } from './characterPassives';

function EventData({
  data,
  catalog,
  kind,
}: {
  readonly data: Record<string, JSONValue>;
  readonly kind: string;
  readonly catalog: ModeCatalog;
}) {
  const t = useDuelText();
  const passiveName = (id: string) => {
    const role = catalog.roles.find((r) => r.passive?.id === id);
    return role ? (characterPassive(role, t)?.name ?? id) : id;
  };
  const labels: Record<string, string> = {
    skillId: t('技能', 'Skill'),
    derived: t('连答', 'Follow-up'),
    main: t('主技能', 'Main skill'),
    energy: t('电能消耗', 'Energy paid'),
    token: t('Token消耗', 'Tokens paid'),
    likes: t('得赞', 'Likes'),
    passiveLikes: t('被动得赞', 'Passive likes'),
    trialPayment: t('试用支付', 'Trial paid'),
    subPayment: t('订阅支付', 'Subscription paid'),
    apiPayment: t('API支付', 'API paid'),
    gold: t('金币', 'Gold'),
    resourceCosts: t('资源消耗', 'Resource costs'),
    templateId: t('蒸馏模板', 'Distilled template'),
    success: t('成功', 'Success'),
    level: t('等级', 'Level'),
    reason: t('原因', 'Reason'),
    item: t('商品', 'Item'),
    price: t('金币价格', 'Gold price'),
    amount: t('数量', 'Amount'),
    target: t('目标', 'Target'),
    requested: t('请求量', 'Requested'),
    actual: t('实际变化', 'Actual change'),
    overflow: t('溢出', 'Overflow'),
    required: t('需求', 'Required'),
    available: t('可用', 'Available'),
    quotes: t('双方报价', 'Both energy quotes'),
    overloaded: t('双方过载状态', 'Both overload states'),
    owner: t('所属席位', 'Owner seat'),
    key: t('状态标识', 'Status'),
    hit: t('命中', 'Hit'),
    reduction: t('减少得赞', 'Likes reduction'),
    reward: t('额外得赞', 'Extra likes'),
    api: t('API余量', 'API reserve'),
    resource: t('资源', 'Resource'),
    before: t('变化前', 'Before'),
    after: t('变化后', 'After'),
    template: t('学习模板', 'Learned template'),
    remaining: t('剩余', 'Remaining'),
    sample: t('样本', 'Sample'),
    layers: kind === 'persist' ? t('持久层数', 'Persistent layers') : t('层数', 'Layers'),
    persistentLayers: t('持久层数', 'Persistent layers'),
    buffId: 'Buff',
    enabled: t('启用', 'Enabled'),
    status: t('状态', 'Status'),
    result: t('结果', 'Result'),
    plans: t('双方方案', 'Both plans'),
    shortage: t('过载原因', 'Overload cause'),
    payment: t('支付方式', 'Payment'),
    resources: t('相关资源', 'Affected resources'),
    characterPassive: t('角色被动', 'Character passive'),
    applications: t('施加结果', 'Applications'),
    step: t('结算步', 'Resolution step'),
    kind: t('类型', 'Kind'),
    index: t('序号（从零开始）', 'Index (zero based)'),
    rules_version: t('规则版本', 'Rule version'),
    source: t('施放席位', 'Source seat'),
    skill_id: t('技能', 'Skill'),
    buff_id: t('效果', 'Effect'),
    layer: t('尝试层', 'Attempted layer'),
    resist: t('抵抗', 'Resistance'),
    resisted: t('抵抗层数', 'Resisted layers'),
    numerator: t('成功区间', 'Successful values'),
    denominator: t('候选数量', 'Candidate count'),
    draw: t('随机抽样序号；空表示必中', 'Draw ordinal; empty means guaranteed'),
  };
  const render = (value: JSONValue, key: string): string => {
    if (value === null) return '—';
    if (typeof value === 'boolean') return value ? t('是', 'Yes') : t('否', 'No');
    if (typeof value === 'number') return String(value);
    if (typeof value === 'string')
      return key === 'characterPassive'
        ? passiveName(value)
        : key === 'kind' && ['main', 'extra', 'flash'].includes(value)
          ? ({
              main: t('主技能', 'Main skill'),
              extra: t('额外技能', 'Extra skill'),
              flash: t('Flash 连答', 'Flash follow-up'),
            }[value] ?? value)
          : key === 'skillId' || key === 'skill_id' || key === 'templateId' || key === 'template'
            ? skillName(catalog, value)
            : key === 'buffId' || key === 'buff_id'
              ? kind === 'persist' && typeof data.layers === 'number'
                ? effectName(
                    catalog,
                    {
                      kind: 'CACHE',
                      buff_id: value,
                      layers: data.layers,
                      persistent_layers: data.layers,
                    },
                    t,
                  )
                : buffName(catalog, value)
              : key === 'reason'
                ? reasonName(value, t)
                : key === 'item'
                  ? shopName(value, t)
                  : key === 'resource'
                    ? resourceName(value, t)
                    : key === 'payment'
                      ? ({
                          energy: t('共享电能不足', 'Shared energy shortage'),
                          api: t('API 余量不足', 'API reserve shortage'),
                          sub: t('订阅额度不足', 'Subscription shortage'),
                          mix: t('Token 不足', 'Token shortage'),
                        }[value] ?? value)
                      : value;
    if (Array.isArray(value)) return value.map((v) => render(v, key)).join(' / ');
    return Object.entries(value)
      .map(([k, v]) => `${labels[k] ?? k}: ${render(v, k)}`)
      .join(' · ');
  };
  return (
    <dl className="likes-event-data">
      {Object.entries(data).map(([key, value]) => (
        <div key={key}>
          <dt>{labels[key] ?? key}</dt>
          <dd>{render(value, key)}</dd>
        </div>
      ))}
    </dl>
  );
}
function EventLine({
  event,
  catalog,
  you,
}: {
  readonly event: LikesEvent;
  readonly catalog: ModeCatalog;
  readonly you: Seat;
}) {
  const t = useDuelText();
  if (event.transition)
    return (
      <details>
        <summary>{stageName('round-start', t)}</summary>
        <FrameChanges
          before={event.transition.before}
          after={event.transition.after}
          catalog={catalog}
        />
      </details>
    );
  const names: Record<string, string> = {
    cast: t('成功施放', 'Successful cast'),
    'skill-cancelled': t('施放取消', 'Cast cancelled'),
    overload: t('过载', 'Overload'),
    shop: t('购物', 'Purchase'),
    charge: t('共享充电', 'Shared charge'),
    cleanse: t('净化／驱散', 'Cleanse / dispel'),
    counter: t('反制', 'Counter'),
    'resource-gain': t('资源获得', 'Resource gain'),
    trial: t('试用额度', 'Trial quota'),
    resource: t('资源变化', 'Resource change'),
    'usage-reset': t('额度重置', 'Quota reset'),
    learn: t('学习', 'Learning'),
    power: t('发电', 'Power generation'),
    conversion: t('缓存转换', 'Cache conversion'),
    'combo-skip': t('连答未施放', 'Follow-up skipped'),
    effect: t('Buff获得', 'Buff applied'),
    'effect-attempt': t('减益命中与抵抗', 'Debuff hit and resistance'),
    persist: t('持久缓存', 'Persistent cache'),
    mode: t('模式变化', 'Mode change'),
    end: t('终局', 'Game ended'),
    reveal: t('方案揭示', 'Plans revealed'),
  };
  return (
    <details className="likes-event">
      <summary>
        <span>
          {event.seat === null
            ? t('全局', 'Global')
            : event.seat === you
              ? t('你', 'You')
              : t('对手', 'Opponent')}
        </span>{' '}
        · {names[event.kind] ?? event.kind}
        {event.cast ? ` · ${skillName(catalog, event.cast.skillId)} +${event.cast.likes} ♥` : ''}
      </summary>
      <EventData data={event.data} catalog={catalog} kind={event.kind} />
      {event.score && (
        <>
          <p>
            {t('基础得赞', 'Base likes')}: {event.score.original}
          </p>
          {event.score.parts.map((part, index) => (
            <p key={index}>
              {part.buff_id
                ? buffName(catalog, part.buff_id)
                : part.key === 'character'
                  ? t('角色被动', 'Character passive')
                  : part.key}
              : {part.amount >= 0 ? '+' : ''}
              {part.amount}
            </p>
          ))}
          <p>
            {t(
              '条件／倍率前／倍率／被动／实得',
              'Conditional / pre-multiplier / multiplier / passive / final',
            )}
            : {event.score.conditional} / {event.score.before_multiplier} / {event.score.multiplier}{' '}
            / {event.score.passive} / <strong>{event.score.final}</strong>
          </p>
        </>
      )}
    </details>
  );
}
export function LikesRoundLog({
  round,
  you,
  catalog,
}: {
  readonly round: DuelRound<LikesView, RoundFacts, LikesEvent[]>;
  readonly you: Seat;
  readonly catalog: ModeCatalog;
}) {
  const t = useDuelText();
  return (
    <div className="likes-round-log">
      {round.startEvents?.map((event) => (
        <EventLine event={event} catalog={catalog} you={you} key={event.id} />
      ))}
      {[you, (1 - you) as Seat].map((seat) => (
        <section key={seat}>
          <h4>{seat === you ? t('你的方案', 'Your plan') : t('对手方案', 'Opponent’s plan')}</h4>
          <PlanSummary catalog={catalog} plan={round.facts.plans[seat]} />
        </section>
      ))}
      <FrameChanges before={round.facts.before} after={round.facts.after} catalog={catalog} />
      <h4>{t('完整结算事件', 'Complete settlement events')}</h4>
      {round.facts.events.map((event) => (
        <EventLine event={event} catalog={catalog} you={you} key={event.id} />
      ))}
      {round.facts.draws.length > 0 && (
        <details>
          <summary>{t('本轮随机抽样', 'Random selections this round')}</summary>
          <ol>
            {round.facts.draws.map((draw) => (
              <li key={draw.ordinal}>
                {t('候选数量', 'Candidate count')}: {draw.candidate_count} ·{' '}
                {t('选中位置（从零开始）', 'Selected position (zero based)')}: {draw.index}
              </li>
            ))}
          </ol>
        </details>
      )}
    </div>
  );
}
