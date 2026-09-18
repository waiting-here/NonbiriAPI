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
  const t = useDuelText();
  return (
    <span className="likes-cost">
      <span>ϟ {skill.energy}</span>
      <span>
        {skill.token} K tokens ·{' '}
        {skill.payment === 'mix'
          ? t('订阅／API', 'Sub / API')
          : skill.payment === 'sub'
            ? t('仅订阅', 'Subscription')
            : 'API'}
      </span>
      {Object.entries(skill.resourceCosts).map(([key, value]) => (
        <span key={key}>
          {resourceName(key, t)} {value}
        </span>
      ))}
      {!!skill.gold && (
        <span>
          {t('金币', 'Gold')} {skill.gold}
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
  const t = useDuelText();
  const [path, setPath] = useState([
    { id: initial || catalog.skills[0].id, level: 'base' as GuideLevel },
  ]);
  const [search, setSearch] = useState('');
  const heading = useRef<HTMLHeadingElement>(null);
  const selected = path[path.length - 1];
  const entry = knowledge(catalog, selected.id, selected.level, t);
  const related = relatedEntries(catalog, entry, t);
  const skill = catalog.skills.find((s) => s.id === selected.id);
  const harness = catalog.harnesses.find((h) => h.id === selected.id);
  const cost = selected.level === 'base' ? skill : catalog.skills.find((s) => s.id === 'PUB41');
  const entries = [...catalog.skills, ...catalog.buffs, ...catalog.harnesses, ...catalog.passives];
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
      title={t('词条手册', 'Field guide')}
      onClose={onClose}
      className="likes-glossary likes-reader"
    >
      <p>
        {catalog.mode === 'quick' ? t('快速模式', 'Quick mode') : t('标准模式', 'Standard mode')} ·{' '}
        {t(
          '阅读不会暂停正式对局计时。点击彩色术语查看关联规则。',
          'Reading does not pause a live game. Select a highlighted term to read its rules.',
        )}
      </p>
      <div className="likes-reader-grid">
        <nav aria-label={t('词条导航', 'Guide navigation')}>
          <label>
            {t('搜索词条', 'Find an entry')}
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
                  {knowledge(catalog, item.id, 'base', t).title}
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
            {t('← 返回上一词条', '← Back to previous entry')}
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
                {skill.owner} · {kindName(skill.kind, t)}
              </p>
              {skill.copyable && (
                <div
                  className="likes-reader-levels"
                  role="group"
                  aria-label={t('技能版本', 'Skill version')}
                >
                  {guideLevels.map((level) => (
                    <button
                      type="button"
                      key={level}
                      aria-pressed={level === selected.level}
                      onClick={() => setPath((p) => [...p.slice(0, -1), { ...selected, level }])}
                    >
                      {levelName(level, t)}
                    </button>
                  ))}
                </div>
              )}
              <h4>{t('基础费用与次数', 'Base costs and uses')}</h4>
              {cost && <SkillCost skill={cost} />}
              <p>
                {selected.level === 'base'
                  ? t('成功次数', 'Successful uses')
                  : t('蒸馏施放次数', 'Distillation casts')}
                : {cost?.maxUses ?? t('不限', 'Unlimited')}
                {selected.level === 'base' && !skill.copyable
                  ? ` · ${t('不可蒸馏', 'Cannot be distilled')}`
                  : ''}
              </p>
              <p>
                {selected.level === 'base'
                  ? t(
                      '以上为原始费用；当前被动、Buff、支付选择与倍速会影响实际费用和得赞。',
                      'These are original costs. Current passives, buffs, payment choices and speed affect actual costs and likes.',
                    )
                  : t(
                      '蒸馏使用上列自身费用，不继承原技能的金币、图像消耗及次数限制。',
                      'Distillation uses its own costs above, without inheriting original gold/image costs or use limits.',
                    )}
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
              <figcaption>{t('玩梗台词', 'Flavor quote')}</figcaption>
              <blockquote>{entry.meme}</blockquote>
            </figure>
          )}
        </article>
        {related.length > 0 && (
          <aside
            className="likes-reader-related"
            aria-label={t('关联解释', 'Related explanations')}
          >
            <h3>{t('关联解释', 'Related explanations')}</h3>
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
