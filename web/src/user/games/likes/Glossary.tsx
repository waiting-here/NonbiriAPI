import { useRef, useState } from 'react';
import { DuelDialog } from '../common/duel/Dialog';
import { useDuelText } from '../common/duel/copy';
import type { ModeCatalog, Skill } from './catalog';
import { kindName, resourceName } from './labels';
import { LikesArt } from './LikesArt';
import { artRegistry } from './art';
import { guideLevels, knowledge, levelName, relatedEntries, type GuideLevel } from './knowledge';
import { GuideText } from './GuideText';
export function SkillCost({ skill }: { readonly skill: Skill }) {
  const text = useDuelText();
  return (
    <span className="likes-cost">
      <span>ϟ {skill.energy}</span>
      <span>
        {skill.token} K tokens ·{' '}
        {skill.payment === 'mix'
          ? text('likes.subAPI')
          : skill.payment === 'sub'
            ? text('likes.subscription')
            : 'API'}
      </span>
      {Object.entries(skill.resourceCosts).map(([key, value]) => (
        <span key={key}>
          {resourceName(key, text)} {value}
        </span>
      ))}
      {!!skill.gold && (
        <span>
          {text('likes.gold')} {skill.gold}
        </span>
      )}
    </span>
  );
}
export function Glossary({
  catalog,
  initial,
  onClose,
}: {
  readonly catalog: ModeCatalog;
  readonly initial?: string;
  readonly onClose: () => void;
}) {
  const text = useDuelText();
  const [path, setPath] = useState([
    { id: initial || catalog.skills[0].id, level: 'base' as GuideLevel },
  ]);
  const [search, setSearch] = useState('');
  const heading = useRef<HTMLHeadingElement>(null);
  const selected = path[path.length - 1];
  const entry = knowledge(catalog, selected.id, selected.level, text);
  const related = relatedEntries(catalog, entry, text);
  const skill = catalog.skills.find((s) => s.id === selected.id);
  const harness = catalog.harnesses.find((h) => h.id === selected.id);
  const cost = selected.level === 'base' ? skill : catalog.skills.find((s) => s.id === 'PUB41');
  const entries = [
    ...catalog.roles.flatMap((r) => (r.passive ? [r.passive] : [])),
    ...catalog.skills,
    ...catalog.buffs,
    ...catalog.harnesses,
    ...catalog.passives,
  ];
  const focus = () =>
    requestAnimationFrame(() => {
      heading.current?.focus();
      heading.current?.scrollIntoView?.({ block: 'nearest' });
    });
  const inspect = (id: string) => {
    setPath((p) => [...p, { id, level: 'base' }]);
    focus();
  };
  return (
    <DuelDialog
      title={text('likes.fieldGuide')}
      onClose={onClose}
      className="likes-glossary likes-reader"
    >
      <p>
        {catalog.mode === 'quick' ? text('likes.quickMode') : text('likes.standardMode')} ·{' '}
        {text('likes.readingDoesNotPauseALiveGame')}
      </p>
      <div className="likes-reader-grid">
        <nav aria-label={text('likes.guideNavigation')}>
          <label>
            {text('likes.findAnEntry')}
            <input value={search} onChange={(e) => setSearch(e.target.value)} maxLength={80} />
          </label>
          <div className="likes-glossary-nav">
            {entries
              .filter((item) => item.name.toLocaleLowerCase().includes(search.toLocaleLowerCase()))
              .map((item) => (
                <button
                  type="button"
                  key={item.id}
                  aria-current={selected.id === item.id ? 'page' : undefined}
                  onClick={() => inspect(item.id)}
                >
                  {knowledge(catalog, item.id, 'base', text).title}
                </button>
              ))}
          </div>
        </nav>
        <article className="likes-reader-main">
          <button
            type="button"
            disabled={path.length <= 1}
            onClick={() => {
              setPath((p) => p.slice(0, -1));
              focus();
            }}
          >
            {text('likes.backToPreviousEntry')}
          </button>
          {harness && (
            <LikesArt
              slot={artRegistry[`harness.${harness.id}`]}
              label={harness.name}
              className="likes-harness-art"
            />
          )}
          <h3 ref={heading} tabIndex={-1}>
            {entry.title}
          </h3>
          {skill && (
            <>
              <p>
                {skill.owner} · {kindName(skill.kind, text)}
              </p>
              {skill.copyable && (
                <div
                  className="likes-reader-levels"
                  role="group"
                  aria-label={text('likes.skillVersion')}
                >
                  {guideLevels.map((level) => (
                    <button
                      type="button"
                      key={level}
                      aria-pressed={level === selected.level}
                      onClick={() => setPath((p) => [...p.slice(0, -1), { ...selected, level }])}
                    >
                      {levelName(level, text)}
                    </button>
                  ))}
                </div>
              )}
              <h4>{text('likes.baseCostsAndUses')}</h4>
              {cost && <SkillCost skill={cost} />}
              <p>
                {selected.level === 'base'
                  ? text('likes.successfulUses')
                  : text('likes.distillationCasts')}
                : {cost?.maxUses ?? text('likes.unlimited')}
                {selected.level === 'base' && !skill.copyable
                  ? ` · ${text('likes.cannotBeDistilled')}`
                  : ''}
              </p>
              <p>
                {selected.level === 'base'
                  ? text('likes.theseAreOriginalCostsCurrentPassivesBuffs')
                  : text('likes.distillationUsesItsOwnCostsAboveWithout')}
              </p>
            </>
          )}
          {entry.paragraphs.map((text, index) => (
            <p key={index}>
              <GuideText catalog={catalog} text={text} onInspect={inspect} />
            </p>
          ))}
          {entry.meme && (
            <figure className="likes-flavor">
              <blockquote>{entry.meme}</blockquote>
            </figure>
          )}
        </article>
        {related.length > 0 && (
          <aside className="likes-reader-related" aria-label={text('likes.relatedExplanations')}>
            <h3>{text('likes.relatedExplanations')}</h3>
            {related.map((item) => (
              <section key={item.id}>
                <h4>
                  <button className="likes-term" type="button" onClick={() => inspect(item.id)}>
                    {item.title} ↗
                  </button>
                </h4>
                {item.paragraphs.map((text, index) => (
                  <p key={index}>
                    <GuideText catalog={catalog} text={text} onInspect={inspect} />
                  </p>
                ))}
              </section>
            ))}
          </aside>
        )}
      </div>
    </DuelDialog>
  );
}
