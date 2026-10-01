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
  const text = useDuelText();
  const passiveName = (id: string) => {
    const role = catalog.roles.find((r) => r.passive?.id === id);
    return role
      ? (characterPassive(role, text)?.name ?? id)
      : (catalog.passives.find((p) => p.id === id)?.name ?? id);
  };
  const labels: Record<string, string> = {
    skillId: text('likes.skill'),
    derived: text('likes.followUp'),
    main: text('likes.mainSkill'),
    energy: text('likes.energyPaid'),
    token: text('likes.tokensPaid'),
    likes: text('likes.likes2'),
    passiveLikes: text('likes.passiveLikes'),
    trialPayment: text('likes.trialPaid'),
    subPayment: text('likes.subscriptionPaid'),
    apiPayment: text('likes.aPIPaid'),
    gold: text('likes.gold'),
    resourceCosts: text('likes.resourceCosts'),
    templateId: text('likes.distilledTemplate'),
    success: text('likes.success'),
    level: text('likes.level'),
    reason: text('likes.reason'),
    item: text('likes.item'),
    price: text('likes.goldPrice'),
    amount: text('likes.amount'),
    target: text('likes.target'),
    requested: text('likes.requested'),
    actual: text('likes.actualChange'),
    overflow: text('likes.overflow'),
    required: text('likes.required'),
    available: text('likes.available'),
    quotes: text('likes.bothEnergyQuotes'),
    overloaded: text('likes.bothOverloadStates'),
    owner: text('likes.ownerSeat'),
    key: text('likes.status'),
    hit: text('likes.hit'),
    reduction: text('likes.likesReduction'),
    reward: text('likes.extraLikes'),
    api: text('likes.aPIReserve'),
    resource: text('likes.resource'),
    before: text('likes.before'),
    after: text('likes.after'),
    template: text('likes.learnedTemplate'),
    remaining: text('likes.remaining'),
    sample: text('likes.sample'),
    layers: kind === 'persist' ? text('likes.persistentLayers2') : text('likes.layers'),
    persistentLayers: text('likes.persistentLayers2'),
    buffId: 'Buff',
    enabled: text('likes.enabled'),
    status: text('likes.status2'),
    result: text('likes.result'),
    plans: text('likes.bothPlans'),
    shortage: text('likes.overloadCause'),
    payment: text('likes.payment'),
    resources: text('likes.affectedResources'),
    passive_id: text('likes.harnessPassive'),
    characterPassive: text('likes.characterPassive'),
    applications: text('likes.applications'),
    step: text('likes.resolutionStep'),
    kind: text('likes.kind'),
    index: text('likes.indexZeroBased'),
    rules_version: text('likes.ruleVersion'),
    source: text('likes.sourceSeat'),
    skill_id: text('likes.skill'),
    buff_id: text('likes.effect'),
    layer: text('likes.attemptedLayer'),
    resist: text('likes.resistance'),
    resisted: text('likes.resistedLayers'),
    numerator: text('likes.successfulValues'),
    denominator: text('likes.candidateCount'),
    draw: text('likes.drawOrdinalEmptyMeansGuaranteed'),
  };
  const render = (value: JSONValue, key: string): string => {
    if (value === null) return '—';
    if (typeof value === 'boolean') return value ? text('likes.yes') : text('likes.no');
    if (typeof value === 'number') return String(value);
    if (typeof value === 'string')
      return key === 'characterPassive' || key === 'passive_id'
        ? passiveName(value)
        : key === 'kind' && ['main', 'extra', 'flash'].includes(value)
          ? ({
              main: text('likes.mainSkill'),
              extra: text('likes.extraSkill'),
              flash: text('likes.flashFollowUp'),
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
                    text,
                  )
                : buffName(catalog, value)
              : key === 'reason'
                ? reasonName(value, text)
                : key === 'item'
                  ? shopName(value, text)
                  : key === 'resource'
                    ? resourceName(value, text)
                    : key === 'payment'
                      ? ({
                          energy: text('likes.sharedEnergyShortage'),
                          api: text('likes.aPIReserveShortage'),
                          sub: text('likes.subscriptionShortage'),
                          mix: text('likes.tokenShortage'),
                          image: text('likes.imageQuotaShortage'),
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
  const text = useDuelText();
  if (event.transition)
    return (
      <details>
        <summary>{stageName('round-start', text)}</summary>
        <FrameChanges
          before={event.transition.before}
          after={event.transition.after}
          catalog={catalog}
        />
      </details>
    );
  const names: Record<string, string> = {
    cast: text('likes.successfulCast'),
    'harness-like': text('likes.harnessBonusLike'),
    'skill-cancelled': text('likes.castCancelled'),
    overload: text('likes.overload'),
    shop: text('likes.purchase'),
    charge: text('likes.sharedCharge'),
    cleanse: text('likes.cleanseDispel'),
    counter: text('likes.counter'),
    'resource-gain': text('likes.resourceGain'),
    trial: text('likes.trialQuota'),
    resource: text('likes.resourceChange'),
    'usage-reset': text('likes.quotaReset'),
    learn: text('likes.learning'),
    power: text('likes.powerGeneration'),
    conversion: text('likes.cacheConversion'),
    'combo-skip': text('likes.followUpSkipped'),
    effect: text('likes.buffApplied'),
    'effect-attempt': text('likes.debuffHitAndResistance'),
    persist: text('likes.persistentCache2'),
    mode: text('likes.modeChange'),
    end: text('likes.gameEnded'),
    reveal: text('likes.plansRevealed'),
  };
  return (
    <details className="likes-event">
      <summary>
        <span>
          {event.seat === null
            ? text('likes.global')
            : event.seat === you
              ? text('bidding.you')
              : text('bidding.opponent')}
        </span>{' '}
        · {names[event.kind] ?? event.kind}
        {event.cast ? ` · ${skillName(catalog, event.cast.skillId)} +${event.cast.likes} ♥` : ''}
      </summary>
      <EventData data={event.data} catalog={catalog} kind={event.kind} />
      {event.score && (
        <>
          <p>
            {text('likes.baseLikes2')}: {event.score.original}
          </p>
          {event.score.parts.map((part, index) => (
            <p key={index}>
              {part.buff_id
                ? buffName(catalog, part.buff_id)
                : part.key === 'character'
                  ? text('likes.characterPassive')
                  : part.key}
              : {part.amount >= 0 ? '+' : ''}
              {part.amount}
            </p>
          ))}
          <p>
            {text('likes.conditionalPreMultiplierMultiplierPassiveFinal')}:{' '}
            {event.score.conditional} / {event.score.before_multiplier} / {event.score.multiplier} /{' '}
            {event.score.passive} / <strong>{event.score.final}</strong>
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
  const text = useDuelText();
  return (
    <div className="likes-round-log">
      {round.startEvents?.map((event) => (
        <EventLine event={event} catalog={catalog} you={you} key={event.id} />
      ))}
      {[you, (1 - you) as Seat].map((seat) => (
        <section key={seat}>
          <h4>{seat === you ? text('likes.yourPlan') : text('likes.opponentSPlan')}</h4>
          <PlanSummary catalog={catalog} plan={round.facts.plans[seat]} />
        </section>
      ))}
      <FrameChanges before={round.facts.before} after={round.facts.after} catalog={catalog} />
      <h4>{text('likes.completeSettlementEvents')}</h4>
      {round.facts.events.map((event) => (
        <EventLine event={event} catalog={catalog} you={you} key={event.id} />
      ))}
      {round.facts.draws.length > 0 && (
        <details>
          <summary>{text('likes.randomSelectionsThisRound')}</summary>
          <ol>
            {round.facts.draws.map((draw) => (
              <li key={draw.ordinal}>
                {text('likes.candidateCount')}: {draw.candidate_count} ·{' '}
                {text('likes.selectedPositionZeroBased')}: {draw.index}
              </li>
            ))}
          </ol>
        </details>
      )}
    </div>
  );
}
