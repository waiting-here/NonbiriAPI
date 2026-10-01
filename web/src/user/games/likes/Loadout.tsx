import { useDuelText } from '../common/duel/copy';
import type { ModeCatalog } from './catalog';
import type { Selection } from './types';
import { LikesArt } from './LikesArt';
import { artRegistry, characterSlot } from './art';
import { kindName } from './labels';
import { SkillCost } from './Glossary';
import { EffectSummary } from './GuideText';
import { selectionProblem } from './selection';
import { CharacterPassive } from './CharacterPassive';
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
  const text = useDuelText(),
    slots = 4 + (catalog.harnesses.find((h) => h.id === value.harness)?.activeSlots ?? 0);
  const problem = selectionProblem(catalog, value),
    role = catalog.roles.find((r) => r.id === value.role)!;
  return (
    <div className="likes-loadout">
      <div className="likes-section-heading">
        <h2>{text('likes.chooseACharacter')}</h2>
        <span>{text('likes.skillsAndResourcesBelongToThisGame')}</span>
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
        {text('likes.weakness')}: {role.weakness}
      </p>
      <CharacterPassive role={role} onInspect={onInspect} />
      <h3>Harness</h3>
      <div className="likes-harness-grid">
        <button
          type="button"
          aria-pressed={value.harness === null}
          disabled={disabled}
          onClick={() => onChange({ ...value, harness: null, skills: value.skills.slice(0, 4) })}
        >
          {text('likes.none2')}
          <small>4 {text('likes.activeSlots')}</small>
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
                +{h.activeSlots} {text('likes.active')} · {h.passives.length}{' '}
                {text('likes.passive2')}
              </small>
            </button>
            <button
              type="button"
              className="likes-info"
              aria-label={`${h.name} ${text('likes.details')}`}
              onClick={() => onInspect(h.id)}
            >
              ⓘ
            </button>
          </div>
        ))}
      </div>
      <div className="likes-section-heading">
        <h3>
          {text('likes.loadout')} {value.skills.length} / {slots}
        </h3>
        <label>
          {text('likes.suggestedSet')}
          <select
            value=""
            disabled={disabled}
            onChange={(e) => {
              const preset = catalog.loadouts.find((p) => p.id === e.target.value);
              if (preset) onChange({ ...value, skills: [...preset.skills] });
            }}
          >
            <option value="">{text('likes.chooseAPreset')}</option>
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
      <p>{text('likes.includeAtLeastOneSustainableStableScoring')}</p>
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
                    {kindName(skill.kind, text)} · {skill.owner}
                  </small>
                  <SkillCost skill={skill} />
                  <EffectSummary catalog={catalog} id={skill.id} harness={value.harness} />
                </span>
              </label>
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
      {problem && (
        <p role="alert" className="likes-warning">
          {problem === 'slots'
            ? text('likes.checkSkillOwnershipDuplicatesAndSlotCount')
            : text('likes.selectAtLeastOneSustainableStableScoring')}
        </p>
      )}
    </div>
  );
}
