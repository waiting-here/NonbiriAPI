import { useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Drawer } from '@shared/components/ui/Drawer';

export type LoadoutStepID = 'mode' | 'role' | 'harness' | 'skills';
export type LoadoutDisclosure = {
  active: LoadoutStepID | null;
  onToggle: (id: LoadoutStepID, open: boolean) => void;
};
export function LoadoutStep({
  id,
  title,
  summary,
  disclosure,
  children,
}: {
  id: LoadoutStepID;
  title: string;
  summary: ReactNode;
  disclosure?: LoadoutDisclosure;
  children: ReactNode;
}) {
  if (!disclosure) return children;
  return (
    <details
      className="nb-fold likes-loadout-step"
      open={disclosure.active === id}
      onToggle={(event) => disclosure.onToggle(id, event.currentTarget.open)}
    >
      <summary>
        <strong>{title}</strong>
        <span>{summary}</span>
      </summary>
      <div className="nb-fold__body">{children}</div>
    </details>
  );
}
const mobileLoadout = () => window.matchMedia('(max-width: 39.999rem)').matches;

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
  disclosure,
  children,
}: {
  readonly children?: ReactNode;
  readonly disclosure?: LoadoutDisclosure;
  readonly catalog: ModeCatalog;
  readonly value: Selection;
  readonly onChange: (value: Selection) => void;
  readonly disabled: boolean;
  readonly onInspect: (id: string) => void;
}) {
  const { t } = useTranslation();
  const [inspectedRole, setInspectedRole] = useState<string | null>(null);
  const detailRole = catalog.roles.find((entry) => entry.id === inspectedRole);
  const text = useDuelText(),
    slots = 4 + (catalog.harnesses.find((h) => h.id === value.harness)?.activeSlots ?? 0);
  const problem = selectionProblem(catalog, value),
    role = catalog.roles.find((r) => r.id === value.role)!;
  return (
    <div className="likes-loadout">
      <LoadoutStep
        id="role"
        title={t('user.games.presentation.stepRole')}
        summary={role.name}
        disclosure={disclosure}
      >
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
              onClick={() => {
                onChange({
                  role: role.id,
                  harness: value.harness,
                  skills: [...catalog.loadouts.find((p) => p.role === role.id)!.skills],
                });
                if (disclosure && mobileLoadout()) setInspectedRole(role.id);
              }}
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
      </LoadoutStep>
      <LoadoutStep
        id="harness"
        title={t('user.games.presentation.stepHarness')}
        summary={
          catalog.harnesses.find((entry) => entry.id === value.harness)?.name ?? text('likes.none2')
        }
        disclosure={disclosure}
      >
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
                onClick={() => {
                  onChange({
                    ...value,
                    harness: h.id,
                    skills: value.skills.slice(0, 4 + h.activeSlots),
                  });
                  if (disclosure && mobileLoadout()) onInspect(h.id);
                }}
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
      </LoadoutStep>
      <LoadoutStep
        id="skills"
        title={t('user.games.presentation.stepSkills')}
        summary={`${value.skills.length} / ${slots}`}
        disclosure={disclosure}
      >
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
                    onChange={(e) => {
                      onChange({
                        ...value,
                        skills: e.target.checked
                          ? [...value.skills, skill.id]
                          : value.skills.filter((id) => id !== skill.id),
                      });
                      if (disclosure && mobileLoadout()) onInspect(skill.id);
                    }}
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
        {children}
      </LoadoutStep>
      {problem && (
        <p role="alert" className="likes-warning">
          {problem === 'slots'
            ? text('likes.checkSkillOwnershipDuplicatesAndSlotCount')
            : text('likes.selectAtLeastOneSustainableStableScoring')}
        </p>
      )}
      <Drawer
        open={!!detailRole}
        title={detailRole?.name ?? ''}
        closeLabel={t('common.close')}
        onClose={() => setInspectedRole(null)}
      >
        {detailRole && (
          <>
            <p>{detailRole.focus}</p>
            <p>
              <strong>{detailRole.difficulty}</strong> · {detailRole.note}
            </p>
            <p>
              {text('likes.weakness')}: {detailRole.weakness}
            </p>
            <CharacterPassive
              role={detailRole}
              onInspect={(id) => {
                setInspectedRole(null);
                onInspect(id);
              }}
            />
          </>
        )}
      </Drawer>
    </div>
  );
}
