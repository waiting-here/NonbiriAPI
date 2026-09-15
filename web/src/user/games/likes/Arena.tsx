import type { Profile, Pair, Resolution, RoundStart, Seat } from '../common/duel/types';
import { DuelProfile } from '../common/duel/Feedback';
import { useDuelText } from '../common/duel/copy';
import type { ModeCatalog } from './catalog';
import type { Frame, LikesEvent, LikesView, Presentation, Resources, Score } from './types';
import { LikesArt } from './LikesArt';
import { artRegistry, castSlot, characterSlot } from './art';
import { buffName, reasonName, resourceName, skillName, stageName } from './labels';
import { arenaMotion, interpolate, overloadCues } from './motion';
import { ResourceMeter } from './ResourceMeter';
import { PlanSummary } from './PlanEditor';
import { CastImpact } from './CastImpact';

export function CompactScores({
  view,
  resolution,
  roundStart,
  now,
  reduced,
  you,
}: {
  readonly view: LikesView;
  readonly resolution: Resolution<Presentation> | null;
  readonly roundStart: RoundStart<LikesEvent[]> | null;
  readonly now: number;
  readonly reduced: boolean;
  readonly you: Seat;
}) {
  const t = useDuelText(),
    { motion } = arenaMotion(view, resolution, roundStart, now, reduced);
  const other = (1 - you) as Seat;
  return (
    <div
      className="likes-compact-scores"
      aria-label={t('实时比分与电能', 'Live scores and energy')}
    >
      <span>
        {t('你', 'You')} ♥{' '}
        <strong>
          {interpolate(
            motion.from.players[you].likes,
            motion.to.players[you].likes,
            motion.progress,
          )}
        </strong>
      </span>
      <span>
        {t('电能', 'Energy')} ϟ{' '}
        <strong>{interpolate(motion.from.energy, motion.to.energy, motion.progress)}</strong>
      </span>
      <span>
        {t('对手', 'Opponent')} ♥{' '}
        <strong>
          {interpolate(
            motion.from.players[other].likes,
            motion.to.players[other].likes,
            motion.progress,
          )}
        </strong>
      </span>
    </div>
  );
}

function ScoreParts({
  score,
  catalog,
  onInspect,
}: {
  readonly score: Score;
  readonly catalog: ModeCatalog;
  readonly onInspect: (id: string) => void;
}) {
  const t = useDuelText();
  return (
    <div className="likes-score-parts">
      <span>
        {t('基础', 'Base')} {score.original}
      </span>
      {score.parts.map((part, i) => (
        <button
          type="button"
          className="likes-tag"
          key={i}
          disabled={!part.buff_id}
          onClick={() => part.buff_id && onInspect(part.buff_id)}
        >
          {part.buff_id
            ? buffName(catalog, part.buff_id)
            : part.key === 'harness'
              ? 'Harness'
              : part.key === 'skill-decay'
                ? t('技能衰减', 'Skill decay')
                : part.key === 'counter'
                  ? t('反制', 'Counter')
                  : part.key === 'condition'
                    ? t('条件修正', 'Conditional')
                    : t('得赞修正', 'Likes adjustment')}{' '}
          {part.amount >= 0 ? '+' : ''}
          {part.amount}
        </button>
      ))}
      {score.conditional !== 0 && !score.parts.some((part) => part.key === 'condition') && (
        <span>
          {t('条件修正', 'Conditional')} {score.conditional >= 0 ? '+' : ''}
          {score.conditional}
        </span>
      )}
      {score.multiplier !== 1 && <span>×{score.multiplier}</span>}
      {score.passive !== 0 && (
        <span>
          {t('被动', 'Passive')} +{score.passive}
        </span>
      )}
      <strong>
        {t('实得', 'Awarded')} {score.final} ♥
      </strong>
    </div>
  );
}
function ResourcePanel({
  from,
  to,
  progress,
  reduced,
  catalog,
  identity,
}: {
  readonly from: Resources;
  readonly to: Resources;
  readonly progress: number;
  readonly reduced: boolean;
  readonly catalog: ModeCatalog;
  readonly identity: string;
}) {
  const t = useDuelText();
  const meters = [
    {
      key: 'likes',
      from: from.likes,
      to: to.likes,
      cap: catalog.parameters.TARGET_LIKES,
      tone: 'likes-meter--likes',
      unit: '',
    },
    {
      key: 'burst',
      from: from.burst,
      to: to.burst,
      cap: to.burst_cap,
      tone: 'likes-meter--sub',
      unit: ' K',
    },
    {
      key: 'sub',
      from: from.sub,
      to: to.sub,
      cap: to.sub_cap,
      tone: 'likes-meter--sub',
      unit: ' K',
    },
    {
      key: 'api',
      from: from.api,
      to: to.api,
      cap: undefined,
      tone: 'likes-meter--api',
      unit: ' K',
    },
    {
      key: 'gold',
      from: from.gold,
      to: to.gold,
      cap: undefined,
      tone: 'likes-meter--gold',
      unit: '',
    },
    ...Object.keys({ ...from.resources, ...to.resources }).map((key) => ({
      key,
      from: from.resources[key] ?? 0,
      to: to.resources[key] ?? 0,
      cap: to.resource_caps[key],
      tone: 'likes-meter--image',
      unit: '',
    })),
    ...(from.trial || to.trial
      ? [
          {
            key: 'trial',
            from: from.trial,
            to: to.trial,
            cap: undefined,
            tone: 'likes-meter--api',
            unit: ' K',
          },
        ]
      : []),
  ];
  const isSubscription = (key: string) => ['burst', 'sub', 'R_IMAGE'].includes(key);
  const renderMeter = (m: (typeof meters)[number]) => (
    <ResourceMeter
      key={`${identity}:${m.key}`}
      label={resourceName(m.key, t)}
      from={m.from}
      to={m.to}
      cap={m.cap}
      progress={progress}
      reduced={reduced}
      tone={m.tone}
      unit={m.unit}
    />
  );
  return (
    <div className="likes-resource-grid">
      {meters.filter((m) => m.key === 'likes').map(renderMeter)}
      <fieldset className="likes-subscription">
        <legend>{t('订阅用量', 'SUBSCRIPTION USAGE')}</legend>
        <div>{meters.filter((m) => isSubscription(m.key)).map(renderMeter)}</div>
      </fieldset>
      {meters.filter((m) => m.key !== 'likes' && !isSubscription(m.key)).map(renderMeter)}
    </div>
  );
}
export function Arena({
  catalog,
  view,
  profiles,
  you,
  round,
  locked,
  resolution,
  roundStart,
  now,
  reduced,
  onInspect,
}: {
  readonly catalog: ModeCatalog;
  readonly view: LikesView;
  readonly profiles: Pair<Profile>;
  readonly you: Seat;
  readonly round: number;
  readonly locked: Pair<boolean>;
  readonly resolution: Resolution<Presentation> | null;
  readonly roundStart: RoundStart<LikesEvent[]> | null;
  readonly now: number;
  readonly reduced: boolean;
  readonly onInspect: (id: string) => void;
}) {
  const t = useDuelText();
  const { running, starting, motion } = arenaMotion(view, resolution, roundStart, now, reduced);
  const visibleFrame = motion.progress >= 0.35 ? motion.to : motion.from;
  const overloadedNow =
    running && resolution
      ? overloadCues(resolution.summary, motion.stage, reduced)
      : [false, false];
  const events =
    running && resolution
      ? resolution.summary.events.filter((event) => reduced || event.stage === motion.stage)
      : [];
  const impact = events.some((event) => event.kind === 'overload')
    ? 'overload'
    : events.some((event) => event.kind === 'charge' || event.kind === 'usage-reset')
      ? 'charge'
      : '';
  return (
    <section
      className={`likes-arena ${impact ? `likes-impact--${impact}` : ''}`}
      aria-label={t('双侧对战', 'Both players')}
      data-stage={motion.stage}
      data-reduced-motion={reduced}
    >
      <div className="likes-battery" key={`${round}:${motion.stage}:battery`}>
        <div className="likes-battery-heading">
          <span>
            {running
              ? stageName(motion.stage, t)
              : starting
                ? stageName('round-start', t)
                : t('共同电池', 'SHARED BATTERY')}
          </span>
          {impact && (
            <strong>
              {impact === 'overload' ? t('电能过载', 'OVERLOAD') : t('资源补充', 'RECHARGING')}
            </strong>
          )}
        </div>
        <ResourceMeter
          label={resourceName('energy', t)}
          from={motion.from.energy}
          to={motion.to.energy}
          cap={catalog.parameters.ENERGY_CAP}
          progress={motion.progress}
          reduced={reduced}
          tone="likes-meter--energy"
        />
      </div>
      <div className="likes-combatants">
        {[you, (1 - you) as Seat].map((seat) => {
          const player = view.players[seat],
            effects = visibleFrame.players[seat].effects;
          const overloaded = overloadedNow[seat] || effects.some((s) => s.kind === 'OVERLOAD'),
            stunned = effects.some((s) => s.kind === 'STUN' && s.active_from <= round);
          const casts = events.filter((e) => e.seat === seat && e.cast),
            groups = [
              ...new Set(
                casts.map((e) => `${e.cast!.skillId}:${e.cast!.templateId}:${e.cast!.level ?? ''}`),
              ),
            ].map((key) =>
              casts.filter(
                (e) => `${e.cast!.skillId}:${e.cast!.templateId}:${e.cast!.level ?? ''}` === key,
              ),
            ),
            group =
              groups[Math.min(groups.length - 1, Math.floor(motion.progress * groups.length))] ??
              [],
            lastCast = group.at(-1)?.cast,
            slot = lastCast ? castSlot(player.role, lastCast.skillId) : null;
          const failures = events.filter(
            (e) =>
              e.seat === seat &&
              (e.kind === 'skill-cancelled' || e.kind === 'combo-skip' || e.kind === 'overload'),
          );
          const harness = catalog.harnesses.find((h) => h.id === player.harness);
          return (
            <article
              className={`likes-player ${overloaded ? 'is-overloaded' : ''} ${stunned ? 'is-stunned' : ''}`}
              key={seat}
            >
              <header>
                <DuelProfile profile={profiles[seat]} you={seat === you} />
                <span className="likes-lock">
                  {running
                    ? t('结算中', 'Resolving')
                    : locked[seat]
                      ? t('已锁定', 'Locked')
                      : t('选择中', 'Choosing')}
                </span>
              </header>
              <div className="likes-character">
                <LikesArt
                  slot={characterSlot(
                    player.role,
                    overloaded ? 'chibi_overloaded' : stunned ? 'chibi_stunned' : 'chibi_idle',
                  )}
                  label={player.role}
                  className="likes-chibi"
                />
                {slot && (
                  <div
                    className="likes-cast"
                    key={`${round}:${motion.stage}:${group.map((c) => c.id).join('-')}`}
                  >
                    <LikesArt
                      slot={slot}
                      label={`${player.role} · ${skillName(catalog, lastCast!.skillId)}`}
                    />
                    <strong>
                      {skillName(catalog, lastCast!.skillId)}
                      {group.length > 1 ? ` ×${group.length}` : ''}
                    </strong>
                    {lastCast?.skillId === 'PUB41' && lastCast.templateId && (
                      <small>
                        {skillName(catalog, lastCast.templateId)} {lastCast.level}
                      </small>
                    )}
                  </div>
                )}
                <div className="likes-character-label">
                  <strong>{player.role}</strong>
                  {harness && (
                    <button
                      type="button"
                      className="likes-harness-badge"
                      onClick={() => onInspect(harness.id)}
                    >
                      <LikesArt slot={artRegistry[`harness.${harness.id}`]} label={harness.name} />
                      {harness.name}
                    </button>
                  )}
                  {overloaded && (
                    <span className="likes-state-warning">{t('过载', 'OVERLOAD')}</span>
                  )}
                  {stunned && (
                    <span className="likes-state-warning">{t('眩晕Buff', 'STUN BUFF')}</span>
                  )}
                </div>
              </div>
              <CastImpact
                key={`${round}:${motion.stage}`}
                events={casts}
                from={motion.from.players[seat].likes}
                to={motion.to.players[seat].likes}
                target={catalog.parameters.TARGET_LIKES}
                progress={motion.progress}
                reduced={reduced}
                overloaded={!!overloadedNow[seat] && impact === 'overload'}
              />
              <ResourcePanel
                from={motion.from.players[seat]}
                to={motion.to.players[seat]}
                progress={motion.progress}
                reduced={reduced}
                catalog={catalog}
                identity={`${round}:${motion.stage}`}
              />
              {casts.length > 0 && (
                <div className="likes-cast-facts">
                  {casts.map((event) => (
                    <div key={event.id}>
                      <strong>
                        {skillName(catalog, event.cast!.skillId)}{' '}
                        {event.cast!.derived ? t('连答', 'Follow-up') : ''} · +{event.cast!.likes} ♥
                      </strong>
                      {event.score && (
                        <ScoreParts score={event.score} catalog={catalog} onInspect={onInspect} />
                      )}
                    </div>
                  ))}
                </div>
              )}
              {failures.map((event) => (
                <p className="likes-warning" key={event.id}>
                  {typeof event.data.skillId === 'string'
                    ? skillName(catalog, event.data.skillId)
                    : t('追加技能', 'Follow-up')}
                  :{' '}
                  {typeof event.data.reason === 'string'
                    ? reasonName(event.data.reason, t)
                    : t('未成功施放', 'Not cast')}
                </p>
              ))}
              {running && resolution && (
                <PlanSummary catalog={catalog} plan={resolution.summary.plans[seat]} />
              )}
              <div className="likes-buffs" aria-label={t('Buff与状态', 'Buffs and states')}>
                {effects.map((effect) => (
                  <button
                    type="button"
                    className={`likes-tag ${effect.kind === 'OVERLOAD' ? 'is-danger' : ''}`}
                    key={effect.key}
                    onClick={() => onInspect(effect.buff_id)}
                  >
                    {buffName(catalog, effect.buff_id)}
                    {effect.layers > 1 ? ` ×${effect.layers}` : ''}
                    <small>
                      {effect.active_from > round
                        ? t('下轮生效', 'Next round')
                        : effect.remaining > 0
                          ? `${effect.remaining} ${t('轮', 'rounds')}`
                          : ''}
                    </small>
                  </button>
                ))}
              </div>
              <details className="likes-loadout-detail">
                <summary>{t('技能槽与资源时钟', 'Skill slots and resource clocks')}</summary>
                <div className="likes-slots">
                  {player.slots.map((id, index) =>
                    id ? (
                      <button
                        type="button"
                        className="likes-tag"
                        key={index}
                        onClick={() => onInspect(id)}
                      >
                        {skillName(catalog, id)}
                      </button>
                    ) : (
                      <span className="likes-unknown-slot" key={index}>
                        {player.fog ? t('未揭示', 'Hidden') : t('空槽', 'Empty')}
                      </span>
                    ),
                  )}
                </div>
                <p>
                  {t('订阅瞬发重置', 'Burst resets')}:{' '}
                  {player.subscription.burstResetAt === null
                    ? t('未开始计时', 'Clock idle')
                    : `${Math.max(0, player.subscription.burstResetAt - player.normalTurns)} ${t('轮后', 'rounds from now')}`}{' '}
                  · {t('总量／图像重置', 'Total / image resets')}:{' '}
                  {player.subscription.totalResetAt === null
                    ? t('未开始计时', 'Clock idle')
                    : `${Math.max(0, player.subscription.totalResetAt - player.normalTurns)} ${t('轮后', 'rounds from now')}`}
                </p>
                {player.distill && (
                  <p>
                    {t('蒸馏', 'Distill')}:{' '}
                    {player.distill.template ? skillName(catalog, player.distill.template) : '—'}{' '}
                    {player.distill.level} · {t('进度', 'Progress')} {player.distill.learning}
                  </p>
                )}
              </details>
            </article>
          );
        })}
      </div>
    </section>
  );
}
export function FrameChanges({
  before,
  after,
  catalog,
}: {
  readonly before: Frame;
  readonly after: Frame;
  readonly catalog: ModeCatalog;
}) {
  const t = useDuelText();
  return (
    <div className="likes-frame-changes">
      <ResourceMeter
        label={resourceName('energy', t)}
        from={before.energy}
        to={after.energy}
        reduced
      />
      {([0, 1] as const).map((seat) => (
        <section key={seat}>
          <h4>
            {t('席位', 'Seat')} {seat + 1}
          </h4>
          <ResourcePanel
            from={before.players[seat]}
            to={after.players[seat]}
            progress={1}
            reduced
            catalog={catalog}
            identity={`log-${seat}`}
          />
          <details>
            <summary>{t('Buff与状态前后', 'Buffs and states before / after')}</summary>
            {(
              [
                ['before', before],
                ['after', after],
              ] as const
            ).map(([phase, value]) => (
              <section key={phase}>
                <strong>{phase === 'before' ? t('变化前', 'Before') : t('变化后', 'After')}</strong>
                {value.players[seat].effects.length === 0 ? (
                  <p>{t('无', 'None')}</p>
                ) : (
                  <ul>
                    {value.players[seat].effects.map((effect) => (
                      <li key={effect.key}>
                        {buffName(catalog, effect.buff_id)} · {t('层数', 'Layers')} {effect.layers}{' '}
                        · {t('剩余轮数', 'Rounds left')} {effect.remaining} ·{' '}
                        {t('生效轮', 'Active from round')} {effect.active_from}
                      </li>
                    ))}
                  </ul>
                )}
              </section>
            ))}
          </details>
        </section>
      ))}
    </div>
  );
}
