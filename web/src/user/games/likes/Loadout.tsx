import { useDuelText } from '../common/duel/copy';
import type { ModeCatalog } from './catalog';
import type { Selection } from './types';
import { LikesArt } from './LikesArt';
import { artRegistry, characterSlot } from './art';
import { kindName } from './labels';
import { SkillCost } from './Glossary';
import { EffectSummary } from './GuideText';
import { selectionProblem } from './selection';
export function LoadoutEditor({
  catalog,
  value,
  onChange,
  disabled,
  onInspect,
}: {
  readonly catalog: ModeCatalog;
  readonly value: Selection;
  readonly onChange: (value: Selection) => void;
  readonly disabled: boolean;
  readonly onInspect: (id: string) => void;
}) {
  const t = useDuelText(),
    slots = 4 + (catalog.harnesses.find((h) => h.id === value.harness)?.activeSlots ?? 0);
  const problem = selectionProblem(catalog, value),
    role = catalog.roles.find((r) => r.id === value.role)!;
  return (
    <div className="likes-loadout">
      <div className="likes-section-heading">
        <h2>{t('选择角色', 'Choose a character')}</h2>
        <span>{t('技能与资源均为游戏设定', 'Skills and resources belong to this game')}</span>
      </div>
      <div className="likes-role-grid">
        {catalog.roles.map((role) => (
          <button
            type="button"
            className="likes-role-option"
            data-guide={`role:${role.id}`}
            key={role.id}
            aria-pressed={value.role === role.id}
            disabled={disabled}
            onClick={() =>
              onChange({
                role: role.id,
                harness: value.harness,
                skills: [...catalog.loadouts.find((p) => p.role === role.id)!.skills],
              })
            }
          >
            <LikesArt slot={characterSlot(role.id, 'portrait')} label={role.name} />
            <strong>{role.name}</strong>
            <small>{role.focus}</small>
          </button>
        ))}
      </div>
      <p className="likes-role-note">
        <strong>{role.difficulty}</strong> · {role.note}
        <br />
        {t('弱点', 'Weakness')}: {role.weakness}
      </p>
      <h3>Harness</h3>
      <div className="likes-harness-grid">
        <button
          type="button"
          aria-pressed={value.harness === null}
          disabled={disabled}
          onClick={() => onChange({ ...value, harness: null, skills: value.skills.slice(0, 4) })}
        >
          {t('不携带', 'None')}
          <small>4 {t('主动槽', 'active slots')}</small>
        </button>
        {catalog.harnesses.map((h) => (
          <div className="likes-harness-option" key={h.id}>
            <button
              type="button"
              aria-pressed={value.harness === h.id}
              data-guide={`harness:${h.id}`}
              disabled={disabled}
              onClick={() =>
                onChange({
                  ...value,
                  harness: h.id,
                  skills: value.skills.slice(0, 4 + h.activeSlots),
                })
              }
            >
              <LikesArt slot={artRegistry[`harness.${h.id}`]} label={h.name} />
              <strong>{h.name}</strong>
              <EffectSummary catalog={catalog} id={h.id} />
              <small>
                +{h.activeSlots} {t('主动槽', 'active')} · {h.passives.length}{' '}
                {t('被动', 'passive')}
              </small>
            </button>
            <button
              type="button"
              className="likes-info"
              aria-label={`${h.name} ${t('详情', 'details')}`}
              onClick={() => onInspect(h.id)}
            >
              ⓘ
            </button>
          </div>
        ))}
      </div>
      <div className="likes-section-heading">
        <h3>
          {t('配装', 'Loadout')} {value.skills.length} / {slots}
        </h3>
        <label>
          {t('推荐配装', 'Suggested set')}
          <select
            value=""
            disabled={disabled}
            onChange={(e) => {
              const preset = catalog.loadouts.find((p) => p.id === e.target.value);
              if (preset) onChange({ ...value, skills: [...preset.skills] });
            }}
          >
            <option value="">{t('选择预设', 'Choose a preset')}</option>
            {catalog.loadouts
              .filter((p) => p.role === value.role || p.role === '任意角色')
              .map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
          </select>
        </label>
      </div>
      <p>
        {t(
          '选择至少一项可持续得赞的稳定技能；其余槽位可以留空。',
          'Include at least one sustainable, stable scoring skill. Other slots may remain empty.',
        )}
      </p>
      <div className="likes-skill-grid">
        {catalog.skills
          .filter((s) => s.owner === value.role || s.owner === '全局公共')
          .map((skill) => (
            <div key={skill.id} className="likes-skill-option">
              <label>
                <input
                  type="checkbox"
                  data-guide={`equip:${skill.id}`}
                  checked={value.skills.includes(skill.id)}
                  disabled={
                    disabled || (!value.skills.includes(skill.id) && value.skills.length >= slots)
                  }
                  onChange={(e) =>
                    onChange({
                      ...value,
                      skills: e.target.checked
                        ? [...value.skills, skill.id]
                        : value.skills.filter((id) => id !== skill.id),
                    })
                  }
                />
                <span>
                  <strong>{skill.name}</strong>
                  <small>
                    {kindName(skill.kind, t)} · {skill.owner}
                  </small>
                  <SkillCost skill={skill} />
                  <EffectSummary catalog={catalog} id={skill.id} harness={value.harness} />
                </span>
              </label>
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
      {problem && (
        <p role="alert" className="likes-warning">
          {problem === 'slots'
            ? t(
                '请检查技能归属、重复项及槽位数量。',
                'Check skill ownership, duplicates and slot count.',
              )
            : t(
                '请至少选择一项可持续得赞的稳定技能。',
                'Select at least one sustainable, stable scoring skill.',
              )}
        </p>
      )}
    </div>
  );
}
