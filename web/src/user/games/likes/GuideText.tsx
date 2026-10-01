import type { ModeCatalog } from './catalog';
import type { GuideLevel } from './knowledge';
import { entryTitle, knowledge } from './knowledge';
import { useDuelText } from '../common/duel/copy';
import { effectCategory } from './characterPassives';
export function GuideText({
  text: prose,
  catalog,
  onInspect,
}: {
  readonly text: string;
  readonly catalog: ModeCatalog;
  readonly onInspect: (id: string) => void;
}) {
  const text = useDuelText();
  return (
    <>
      {prose.split(/(\[\[[^\]]+\]\])/g).map((part, index) =>
        part.startsWith('[[') ? (
          <button
            type="button"
            className="likes-term"
            key={index}
            onClick={() => onInspect(part.slice(2, -2))}
          >
            {entryTitle(catalog, part.slice(2, -2), text)}
          </button>
        ) : (
          part
        ),
      )}
    </>
  );
}
export function EffectSummary({
  catalog,
  id,
  level = 'base',
  harness,
}: {
  readonly catalog: ModeCatalog;
  readonly id: string;
  readonly level?: GuideLevel;
  readonly harness?: string | null;
}) {
  const text = useDuelText();
  const entry = knowledge(catalog, id, level, text);
  const buff = catalog.buffs.find((b) => b.id === id);
  const summary = entry.summary.replace(/\[\[([^\]]+)\]\]/g, (_, ref: string) =>
    entryTitle(catalog, ref, text),
  );
  const skill = catalog.skills.find((s) => s.id === id);
  const strength = catalog.harnesses
    .find((h) => h.id === harness)
    ?.passives.map((pid) => catalog.passives.find((p) => p.id === pid))
    .find((p) => p?.kind === 'LIKE_STRENGTH');
  const bonus =
    skill && strength && skill.energy > 0 && skill.token > 0 && skill.effects[level].likes > 0
      ? strength.p + (level === 'base' && skill.image > 0 ? strength.q : 0)
      : 0;
  return (
    <span className="likes-effect-summary">
      {buff && <strong className="likes-effect-category">{effectCategory(buff, text)} · </strong>}
      {summary}
      {bonus > 0 && (
        <span className="likes-base-bonus">
          {text('likes.currentHarnessAddsBaseLikes', { bonus: bonus })}
        </span>
      )}
      {entry.meme && <q className="likes-inline-flavor">{entry.meme}</q>}
    </span>
  );
}
