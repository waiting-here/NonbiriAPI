import { useState } from 'react';
import { DuelDialog } from '../common/duel/Dialog';
import { useDuelText } from '../common/duel/copy';
import type { ModeCatalog, Skill } from './catalog';
import { buffName, kindName, resourceName, skillName } from './labels';
import { LikesArt } from './LikesArt';
import { artRegistry } from './art';
import { buffFacts, effectFacts } from './effectFacts';

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
  const [selected, setSelected] = useState(initial ?? catalog.skills[0].id);
  const [search, setSearch] = useState('');
  const skill = catalog.skills.find((s) => s.id === selected),
    buff = catalog.buffs.find((s) => s.id === selected),
    harness = catalog.harnesses.find((s) => s.id === selected),
    passive = catalog.passives.find((s) => s.id === selected);
  const entries = [...catalog.skills, ...catalog.buffs, ...catalog.harnesses, ...catalog.passives];
  return (
    <DuelDialog title={t('词条手册', 'Field guide')} onClose={onClose} className="likes-glossary">
      <p>
        {t(
          '手册与本局规则版本一致。阅读不会暂停倒计时。',
          'This guide matches the game’s rules. Reading does not pause the countdown.',
        )}
      </p>
      <div className="likes-glossary-grid">
        <nav aria-label={t('词条导航', 'Guide navigation')}>
          <label>
            {t('搜索词条', 'Find an entry')}
            <input value={search} onChange={(e) => setSearch(e.target.value)} maxLength={80} />
          </label>
          <div className="likes-glossary-nav">
            {entries
              .filter((item) =>
                `${item.id} ${item.name}`.toLocaleLowerCase().includes(search.toLocaleLowerCase()),
              )
              .map((item) => (
                <button
                  type="button"
                  key={item.id}
                  aria-current={selected === item.id ? 'page' : undefined}
                  onClick={() => setSelected(item.id)}
                >
                  {item.name}
                </button>
              ))}
          </div>
        </nav>
        <article aria-live="polite">
          {skill && (
            <>
              <h3>{skill.name}</h3>
              <p>
                {skill.owner} · {kindName(skill.kind, t)}
              </p>
              <SkillCost skill={skill} />
              <p>{skill.note}</p>
              <p>{skill.meme}</p>
              <p>
                {t('可用次数', 'Uses')}: {skill.maxUses ?? t('不限', 'Unlimited')} ·{' '}
                {t('稳定技能', 'Stable')}: {skill.stable ? t('是', 'Yes') : t('否', 'No')}
              </p>
              <p>
                {t(
                  '此处显示基础费用；Buff和付款方式可能影响最终消耗，实际结果以结算为准。',
                  'These are base costs. Buffs and payment choices may change the final cost; settlement shows what was actually paid.',
                )}
              </p>
              {(['base', ...(skill.copyable ? ['I', 'II'] : [])] as ('base' | 'I' | 'II')[]).map(
                (level) => {
                  const fx = skill.effects[level];
                  return (
                    <section key={level} className="likes-glossary-effect">
                      <h4>
                        {level === 'base'
                          ? t('原版', 'Original')
                          : `${t('蒸馏', 'Distilled')} ${level}`}
                      </h4>
                      <dl>
                        <div>
                          <dt>{t('基础得赞', 'Base likes')}</dt>
                          <dd>{fx.likes}</dd>
                        </div>
                        {effectFacts(fx, t).map(([name, value]) => (
                          <div key={name}>
                            <dt>{name}</dt>
                            <dd>{value}</dd>
                          </div>
                        ))}
                      </dl>
                      <p>{fx.meme}</p>
                      {[fx.buffId, fx.extraBuffId]
                        .filter((id): id is string => !!id)
                        .map((id) => (
                          <button
                            type="button"
                            className="likes-tag"
                            key={id}
                            onClick={() => setSelected(id)}
                          >
                            {buffName(catalog, id)} ↗
                          </button>
                        ))}
                      {fx.cacheTarget && (
                        <button
                          type="button"
                          className="likes-tag"
                          onClick={() => setSelected(fx.cacheTarget!)}
                        >
                          {skillName(catalog, fx.cacheTarget)} ↗
                        </button>
                      )}
                    </section>
                  );
                },
              )}
            </>
          )}
          {buff && (
            <>
              <h3>{buff.name}</h3>
              <p>{buff.category === 'state' ? t('状态', 'State') : 'Buff'}</p>
              <p>{buff.description}</p>
              <dl>
                {[
                  [t('触发', 'Trigger'), buff.trigger],
                  [t('持续与结束', 'Duration and expiry'), buff.expiry],
                  [t('叠加与覆盖', 'Stacking'), buff.overwrite],
                  [t('作用对象', 'Target'), buff.target],
                ].map(([title, body]) => (
                  <div key={title}>
                    <dt>{title}</dt>
                    <dd>{body}</dd>
                  </div>
                ))}
              </dl>
              <dl>
                {buffFacts(buff, t).map(([name, value]) => (
                  <div key={name}>
                    <dt>{name}</dt>
                    <dd>{value}</dd>
                  </div>
                ))}
              </dl>
              <p>{buff.meme}</p>
            </>
          )}
          {harness && (
            <>
              <LikesArt
                slot={artRegistry[`harness.${harness.id}`]}
                label={harness.name}
                className="likes-harness-art"
              />
              <h3>{harness.name}</h3>
              <p>{harness.description}</p>
              <p>
                {t('额外主动槽', 'Extra active slots')}: {harness.activeSlots}
              </p>
              {harness.passives.map((id) => (
                <button
                  type="button"
                  className="likes-tag"
                  key={id}
                  onClick={() => setSelected(id)}
                >
                  {catalog.passives.find((p) => p.id === id)?.name} ↗
                </button>
              ))}
              <p>{harness.meme}</p>
            </>
          )}
          {passive && (
            <>
              <h3>{passive.name}</h3>
              <p>{passive.description}</p>
              <p>{passive.meme}</p>
              {passive.buffId && (
                <button
                  type="button"
                  className="likes-tag"
                  onClick={() => setSelected(passive.buffId!)}
                >
                  {buffName(catalog, passive.buffId)} ↗
                </button>
              )}
            </>
          )}
        </article>
      </div>
    </DuelDialog>
  );
}
