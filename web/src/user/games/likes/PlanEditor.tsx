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
  const text = useDuelText();
  const describe = (choice: Choice) =>
    [
      skillName(catalog, choice.skillId),
      choice.pay === 'api' ? text('likes.aPIPayment') : text('likes.automaticPayment'),
      choice.cleanseMode
        ? choice.cleanseMode === 'self'
          ? text('likes.cleanseSelf')
          : text('likes.dispelOpponent')
        : '',
      ...(choice.targets ?? []).map((id) => buffName(catalog, id)),
    ]
      .filter(Boolean)
      .join(' · ');
  return (
    <div className="likes-plan-summary">
      <span>
        {text('likes.shop')}:{' '}
        {plan.purchases.length
          ? plan.purchases
              .map(
                (p) =>
                  `${shopName(p.item, text)}${p.target ? ` · ${buffName(catalog, p.target)}` : ''}`,
              )
              .join(' + ')
          : text('likes.none')}
      </span>
      <span>
        {text('likes.main')}:{' '}
        {plan.main ? describe(plan.main) : (emptyMainLabel ?? text('likes.skip'))}
      </span>
      {plan.extra.map((choice, i) => (
        <span key={i}>
          {text('likes.extra')}: {describe(choice)}
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
  const text = useDuelText(),
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
          {text('likes.payment2')}
          <select
            data-guide="payment"
            value={value.pay ?? 'auto'}
            disabled={disabled}
            onChange={(e) => onChange({ ...value, pay: e.target.value === 'api' ? 'api' : 'auto' })}
          >
            <option value="auto">{text('likes.automatic')}</option>
            <option value="api">{text('likes.useAPIReserve')}</option>
          </select>
        </label>
      )}
      {effect?.kind === 'CLEANSE_OR_DISPEL' && (
        <label>
          {text('likes.targetSide')}
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
            <option value="self">{text('likes.cleanseOwnDebuffs')}</option>
            <option value="opponent">{text('likes.dispelOpponentBuffs')}</option>
          </select>
        </label>
      )}
      {removable && !effect?.randomTargets && (
        <fieldset disabled={disabled}>
          <legend>
            {text('likes.chooseTargetBuffs')} · {text('likes.upTo')} {effect?.p}
          </legend>
          {statuses.length === 0 ? (
            <p>{text('likes.noEligibleTargetsRightNow')}</p>
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
          {text('likes.currentDistilledTemplate')}:{' '}
          {player.distill?.template
            ? `${skillName(catalog, player.distill.template)} ${player.distill.level ?? ''}`
            : text('likes.notLearned')}{' '}
          · {text('likes.learning2')}: {player.distill?.learning ?? 0}
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
  const text = useDuelText(),
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
        <h2>{locked ? text('likes.planLocked') : text('likes.thisRoundSPlan')}</h2>
        <span>{locked ? text('likes.waitingForTheReveal') : text('likes.chooseThenConfirm')}</span>
      </div>
      <div className="likes-plan-confirm">
        <PlanSummary
          catalog={catalog}
          plan={plan}
          emptyMainLabel={!skipCasting && !locked ? text('likes.chooseASkill') : undefined}
        />
        <button
          type="button"
          className="likes-primary"
          data-guide="lock"
          disabled={disabled || !affordable || (!skipCasting && !plan.main)}
          onClick={() => onLock(plan)}
        >
          {locked
            ? text('bidding.locked')
            : skipCasting
              ? text('likes.skipCasting')
              : text('likes.lockInPlan')}
        </button>
        {!locked && !affordable && (
          <p className="likes-warning">{text('likes.notEnoughGoldAdjustYourPurchases')}</p>
        )}
        {!locked && !skipCasting && !plan.main && (
          <p>{text('likes.chooseAMainSkillForThisRound')}</p>
        )}
      </div>
      {player.overloaded && (
        <p className="likes-warning">
          {text('likes.overloadedShoppingAndCastingAreUnavailableThis')}
        </p>
      )}
      {player.stunned && (
        <p className="likes-warning">{text('likes.stunIsActiveYouMayCleanseIt')}</p>
      )}
      <div className="likes-tabs" role="group" aria-label={text('likes.planSections')}>
        <button
          type="button"
          data-guide="tab:skills"
          aria-pressed={tab === 'skills'}
          onClick={() => setTab('skills')}
        >
          {text('likes.skills')}
        </button>
        <button
          type="button"
          data-guide="tab:shop"
          aria-pressed={tab === 'shop'}
          onClick={() => setTab('shop')}
        >
          {text('likes.shop')} ({plan.purchases.length}/{catalog.parameters.PREP_MAX})
        </button>
      </div>
      {tab === 'shop' ? (
        <div className="likes-shop">
          <p>{text('likes.bothPlayersShopBeforeSkillCostsAre')}</p>
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
                  <strong>{shopName(item, text)}</strong>
                  <span>
                    {prices[item]} {text('likes.baseGold')}
                  </span>
                  <small>
                    {item === 'api'
                      ? `${player.apiPack} K tokens`
                      : item === 'charge'
                        ? `+${catalog.parameters.CHARGE_PACK} ϟ`
                        : item === 'sub'
                          ? `+${catalog.parameters.SUB_BURST_UPGRADE} / +${catalog.parameters.SUB_TOTAL_UPGRADE} K`
                          : item === 'regulator'
                            ? `${text('likes.energyReduction')} ${catalog.parameters.REGULATOR_SAVE}`
                            : text('likes.removeOneDebuff')}
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
                    {selected ? text('likes.remove') : text('likes.addToPlan')}
                  </button>
                  {item === 'cleanse' && selected && (
                    <select
                      aria-label={text('likes.cleansingTarget')}
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
                    {kindName(skill.kind, text)} ·{' '}
                    {skill.maxUses === null
                      ? text('likes.unlimited2')
                      : `${Math.max(0, skill.maxUses - (player.used[skill.id] ?? 0))} ${text('likes.usesLeft')}`}
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
                  aria-label={`${skill.name} ${text('likes.details')}`}
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
                {text('likes.preselectAnExtraSkill')}
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
                  <option value="">{text('likes.noExtraSkill')}</option>
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
