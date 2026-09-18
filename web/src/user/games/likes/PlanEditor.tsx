import { useState } from 'react';
import type { DuelState } from '../common/duel/types';
import { useDuelText } from '../common/duel/copy';
import type { ModeCatalog } from './catalog';
import type { Choice, LikesEvent, LikesView, Plan, Presentation, Purchase } from './types';
import { buffName, chosenEffect, kindName, shopName, skillName } from './labels';
import { SkillCost } from './Glossary';
import { EffectSummary } from './GuideText';
import { planControls } from './planControls';

export function PlanSummary({
  catalog,
  plan,
  emptyMainLabel,
}: {
  readonly catalog: ModeCatalog;
  readonly plan: Plan;
  readonly emptyMainLabel?: string;
}) {
  const t = useDuelText();
  const describe = (choice: Choice) =>
    [
      skillName(catalog, choice.skillId),
      choice.pay === 'api' ? t('API支付', 'API payment') : t('自动支付', 'Automatic payment'),
      choice.cleanseMode
        ? choice.cleanseMode === 'self'
          ? t('净化自身', 'Cleanse self')
          : t('驱散对手', 'Dispel opponent')
        : '',
      ...(choice.targets ?? []).map((id) => buffName(catalog, id)),
    ]
      .filter(Boolean)
      .join(' · ');
  return (
    <div className="likes-plan-summary">
      <span>
        {t('购物', 'Shop')}:{' '}
        {plan.purchases.length
          ? plan.purchases
              .map(
                (p) =>
                  `${shopName(p.item, t)}${p.target ? ` · ${buffName(catalog, p.target)}` : ''}`,
              )
              .join(' + ')
          : t('无', 'None')}
      </span>
      <span>
        {t('主招', 'Main')}:{' '}
        {plan.main ? describe(plan.main) : (emptyMainLabel ?? t('跳过', 'Skip'))}
      </span>
      {plan.extra.map((choice, i) => (
        <span key={i}>
          {t('额外招', 'Extra')}: {describe(choice)}
        </span>
      ))}
    </div>
  );
}
function ChoiceOptions({
  catalog,
  state,
  value,
  onChange,
  disabled,
}: {
  readonly catalog: ModeCatalog;
  readonly state: DuelState<LikesView, Presentation, LikesEvent[]>;
  readonly value: Choice;
  readonly onChange: (v: Choice) => void;
  readonly disabled: boolean;
}) {
  const t = useDuelText(),
    player = state.view.players[state.you],
    skill = catalog.skills.find((s) => s.id === value.skillId)!;
  const effect = chosenEffect(catalog, player, value);
  const removable =
    effect?.kind === 'CLEANSE_OR_DISPEL' || effect?.kind === 'CLEANSE' || effect?.kind === 'DISPEL';
  const side = effect?.kind === 'DISPEL' ? 'opponent' : (value.cleanseMode ?? 'self');
  const statuses = state.view.players[side === 'self' ? state.you : 1 - state.you].effects.filter(
    (s) => s.category !== 'state' && (side === 'self' ? !s.positive : s.positive),
  );
  return (
    <div className="likes-choice-options">
      {skill.payment === 'mix' && (
        <label>
          {t('付款方式', 'Payment')}
          <select
            data-guide="payment"
            value={value.pay ?? 'auto'}
            disabled={disabled}
            onChange={(e) => onChange({ ...value, pay: e.target.value === 'api' ? 'api' : 'auto' })}
          >
            <option value="auto">{t('自动分配', 'Automatic')}</option>
            <option value="api">{t('使用API余量', 'Use API reserve')}</option>
          </select>
        </label>
      )}
      {effect?.kind === 'CLEANSE_OR_DISPEL' && (
        <label>
          {t('作用方向', 'Target side')}
          <select
            value={value.cleanseMode ?? 'self'}
            disabled={disabled}
            onChange={(e) =>
              onChange({
                ...value,
                cleanseMode: e.target.value === 'opponent' ? 'opponent' : 'self',
                targets: [],
              })
            }
          >
            <option value="self">{t('净化自身负面Buff', 'Cleanse own debuffs')}</option>
            <option value="opponent">{t('驱散对手正面Buff', 'Dispel opponent buffs')}</option>
          </select>
        </label>
      )}
      {removable && !effect?.randomTargets && (
        <fieldset disabled={disabled}>
          <legend>
            {t('选择目标Buff', 'Choose target buffs')} · {t('最多', 'Up to')} {effect?.p}
          </legend>
          {statuses.length === 0 ? (
            <p>{t('当前没有可选目标。', 'No eligible targets right now.')}</p>
          ) : (
            statuses.map((s) => (
              <label className="likes-check" key={s.key}>
                <input
                  type="checkbox"
                  data-guide={`target:${s.key}`}
                  checked={value.targets?.includes(s.key) ?? false}
                  disabled={
                    !value.targets?.includes(s.key) &&
                    (value.targets?.length ?? 0) >= (effect?.p ?? 0)
                  }
                  onChange={(e) =>
                    onChange({
                      ...value,
                      targets: e.target.checked
                        ? [...(value.targets ?? []), s.key]
                        : (value.targets ?? []).filter((key) => key !== s.key),
                    })
                  }
                />
                {s.name} ×{s.layers}
              </label>
            ))
          )}
        </fieldset>
      )}
      {value.skillId === 'PUB41' && (
        <p>
          {t('当前蒸馏模板', 'Current distilled template')}:{' '}
          {player.distill?.template
            ? `${skillName(catalog, player.distill.template)} ${player.distill.level ?? ''}`
            : t('尚未学习', 'Not learned')}{' '}
          · {t('学习进度', 'Learning')}: {player.distill?.learning ?? 0}
        </p>
      )}
    </div>
  );
}
export function PlanEditor({
  catalog,
  state,
  blocked,
  onLock,
  onInspect,
  onSelect,
}: {
  readonly catalog: ModeCatalog;
  readonly state: DuelState<LikesView, Presentation, LikesEvent[]>;
  readonly blocked: boolean;
  readonly onLock: (plan: Plan) => void;
  readonly onInspect: (id: string) => void;
  readonly onSelect?: () => void;
}) {
  const t = useDuelText(),
    player = state.view.players[state.you];
  const [draft, setDraft] = useState<Plan>({ purchases: [], main: null, extra: [] });
  const [tab, setTab] = useState<'skills' | 'shop'>('skills');
  const locked = state.locked[state.you],
    originalPlan = locked && state.view.lockedPlan ? state.view.lockedPlan : draft;
  const { affordable, skipCasting } = planControls(catalog, player, state.round, originalPlan);
  const plan = !locked && skipCasting ? { ...draft, main: null, extra: [] } : originalPlan;
  const disabled = blocked || locked || state.phase !== 'plan' || player.overloaded;
  const effect = chosenEffect(catalog, player, plan.main),
    allowExtra = effect?.kind === 'INSERT';
  const skills = catalog.skills.filter((sk) => player.loadout?.includes(sk.id));
  const prices = {
    sub: catalog.parameters.SUB_PRICE,
    api: catalog.parameters.API_PRICE,
    charge: catalog.parameters.CHARGE_PRICE,
    cleanse: catalog.parameters.CLEANSE_PRICE,
    regulator: catalog.parameters.REGULATOR_PRICE,
  };
  const cleansable = player.effects.filter((s) => s.category !== 'state' && !s.positive);
  const addPurchase = (purchase: Purchase) => {
    setDraft({ ...draft, purchases: [...draft.purchases, purchase] });
    onSelect?.();
  };
  const selectMain = (main: Choice | null) => {
    setDraft({ ...draft, main, extra: [] });
    onSelect?.();
  };
  return (
    <section className="likes-plan-editor">
      <div className="likes-section-heading">
        <h2>{locked ? t('方案已锁定', 'Plan locked') : t('本轮方案', 'This round’s plan')}</h2>
        <span>
          {locked
            ? t('等待双方揭示', 'Waiting for the reveal')
            : t('选择后确认锁定', 'Choose, then confirm')}
        </span>
      </div>
      <div className="likes-plan-confirm">
        <PlanSummary
          catalog={catalog}
          plan={plan}
          emptyMainLabel={!skipCasting && !locked ? t('待选择', 'Choose a skill') : undefined}
        />
        <button
          type="button"
          className="likes-primary"
          data-guide="lock"
          disabled={disabled || !affordable || (!skipCasting && !plan.main)}
          onClick={() => onLock(plan)}
        >
          {locked
            ? t('已锁定', 'Locked')
            : skipCasting
              ? t('跳过出招', 'Skip casting')
              : t('确认方案', 'Lock in plan')}
        </button>
        {!locked && !affordable && (
          <p className="likes-warning">
            {t('金币不足，请调整购物方案。', 'Not enough gold. Adjust your purchases.')}
          </p>
        )}
        {!locked && !skipCasting && !plan.main && (
          <p>{t('请选择本轮主招。', 'Choose a main skill for this round.')}</p>
        )}
      </div>
      {player.overloaded && (
        <p className="likes-warning">
          {t(
            '过载中，本轮无法购物或施放技能。',
            'Overloaded: shopping and casting are unavailable this round.',
          )}
        </p>
      )}
      {player.stunned && (
        <p className="likes-warning">
          {t(
            '眩晕Buff生效。可先使用净化道具解除，再选择技能；否则本轮跳过出招。',
            'Stun is active. You may cleanse it with an item before choosing a skill; otherwise skip casting this round.',
          )}
        </p>
      )}
      <div className="likes-tabs" role="group" aria-label={t('方案编辑区', 'Plan sections')}>
        <button
          type="button"
          data-guide="tab:skills"
          aria-pressed={tab === 'skills'}
          onClick={() => setTab('skills')}
        >
          {t('技能', 'Skills')}
        </button>
        <button
          type="button"
          data-guide="tab:shop"
          aria-pressed={tab === 'shop'}
          onClick={() => setTab('shop')}
        >
          {t('购物', 'Shop')} ({plan.purchases.length}/{catalog.parameters.PREP_MAX})
        </button>
      </div>
      {tab === 'shop' ? (
        <div className="likes-shop">
          <p>
            {t(
              '购物在技能费用检查前共同结算。充电会增加双方共用的电池；净化与稳压器占用同一类购买名额。',
              'Both players shop before skill costs are checked. Charging fills the shared battery. Cleansing and regulators share one purchase category.',
            )}
          </p>
          <div className="likes-shop-grid">
            {(['sub', 'api', 'charge', 'cleanse', 'regulator'] as const).map((item) => {
              const selected = plan.purchases.some((p) => p.item === item),
                categoryUsed = plan.purchases.some((p) =>
                  item === 'cleanse' || item === 'regulator'
                    ? p.item === 'cleanse' || p.item === 'regulator'
                    : p.item === item,
                );
              const unavailable =
                disabled ||
                plan.purchases.length >= catalog.parameters.PREP_MAX ||
                categoryUsed ||
                (item === 'sub' && player.role === 'DeepSeek') ||
                (item === 'charge' && state.view.energy >= catalog.parameters.ENERGY_CAP) ||
                (item === 'cleanse' && cleansable.length === 0);
              return (
                <div key={item}>
                  <strong>{shopName(item, t)}</strong>
                  <span>
                    {prices[item]} {t('基础金币', 'base gold')}
                  </span>
                  <small>
                    {item === 'api'
                      ? `${player.apiPack} K tokens`
                      : item === 'charge'
                        ? `+${catalog.parameters.CHARGE_PACK} ϟ`
                        : item === 'sub'
                          ? `+${catalog.parameters.SUB_BURST_UPGRADE} / +${catalog.parameters.SUB_TOTAL_UPGRADE} K`
                          : item === 'regulator'
                            ? `${t('省电', 'Energy reduction')} ${catalog.parameters.REGULATOR_SAVE}`
                            : t('移除一个负面Buff', 'Remove one debuff')}
                  </small>
                  <button
                    type="button"
                    data-guide={`buy:${item}`}
                    disabled={(unavailable && !selected) || disabled}
                    onClick={() =>
                      selected
                        ? setDraft({
                            ...draft,
                            purchases: draft.purchases.filter((p) => p.item !== item),
                          })
                        : addPurchase({
                            item,
                            ...(item === 'cleanse' ? { target: cleansable[0]?.key } : {}),
                          })
                    }
                  >
                    {selected ? t('移除', 'Remove') : t('加入方案', 'Add to plan')}
                  </button>
                  {item === 'cleanse' && selected && (
                    <select
                      aria-label={t('净化目标', 'Cleansing target')}
                      disabled={disabled}
                      value={plan.purchases.find((p) => p.item === 'cleanse')?.target ?? ''}
                      onChange={(e) =>
                        setDraft({
                          ...draft,
                          purchases: draft.purchases.map((p) =>
                            p.item === 'cleanse' ? { item: 'cleanse', target: e.target.value } : p,
                          ),
                        })
                      }
                    >
                      {cleansable.map((s) => (
                        <option key={s.key} value={s.key}>
                          {s.name}
                        </option>
                      ))}
                    </select>
                  )}
                </div>
              );
            })}
          </div>
        </div>
      ) : (
        <>
          <div className="likes-skill-grid">
            {skills.map((skill) => (
              <div key={skill.id} className="likes-skill-option">
                <button
                  type="button"
                  disabled={
                    disabled ||
                    skipCasting ||
                    (skill.maxUses !== null && (player.used[skill.id] ?? 0) >= skill.maxUses)
                  }
                  aria-pressed={plan.main?.skillId === skill.id}
                  onClick={() => selectMain({ skillId: skill.id, pay: 'auto' })}
                  data-guide={`cast:${skill.id}`}
                >
                  <strong>{skill.name}</strong>
                  <small>
                    {kindName(skill.kind, t)} ·{' '}
                    {skill.maxUses === null
                      ? t('不限次数', 'Unlimited')
                      : `${Math.max(0, skill.maxUses - (player.used[skill.id] ?? 0))} ${t('次剩余', 'uses left')}`}
                  </small>
                  <SkillCost skill={skill} />
                  <EffectSummary
                    catalog={catalog}
                    id={
                      skill.id === 'PUB41' && player.distill?.template
                        ? player.distill.template
                        : skill.id
                    }
                    level={skill.id === 'PUB41' ? (player.distill?.level ?? 'base') : 'base'}
                    harness={player.harness}
                  />
                </button>
                <button
                  type="button"
                  className="likes-info"
                  aria-label={`${skill.name} ${t('详情', 'details')}`}
                  onClick={() => onInspect(skill.id)}
                >
                  ⓘ
                </button>
              </div>
            ))}
          </div>
          {plan.main && (
            <ChoiceOptions
              catalog={catalog}
              state={state}
              value={plan.main}
              disabled={disabled}
              onChange={(main) => setDraft({ ...draft, main })}
            />
          )}
          {allowExtra && (
            <div className="likes-extra">
              <label>
                {t('预选额外技能', 'Preselect an extra skill')}
                <select
                  disabled={disabled}
                  value={plan.extra[0]?.skillId ?? ''}
                  onChange={(e) =>
                    setDraft({
                      ...draft,
                      extra: e.target.value ? [{ skillId: e.target.value, pay: 'auto' }] : [],
                    })
                  }
                >
                  <option value="">{t('不追加', 'No extra skill')}</option>
                  {skills.map((sk) => (
                    <option key={sk.id} value={sk.id}>
                      {sk.name}
                    </option>
                  ))}
                </select>
              </label>
              {plan.extra[0] && (
                <ChoiceOptions
                  catalog={catalog}
                  state={state}
                  value={plan.extra[0]}
                  disabled={disabled}
                  onChange={(extra) => setDraft({ ...draft, extra: [extra] })}
                />
              )}
            </div>
          )}
        </>
      )}
    </section>
  );
}
