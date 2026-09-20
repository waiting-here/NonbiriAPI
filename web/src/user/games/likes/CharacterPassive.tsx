import type { Role } from './catalog';
import { characterPassive } from './characterPassives';
import { useDuelText } from '../common/duel/copy';

export function CharacterPassive({
  role,
  onInspect,
}: {
  readonly role: Role;
  readonly onInspect?: (id: string) => void;
}) {
  const t = useDuelText(),
    content = characterPassive(role, t);
  if (!content || !role.passive) return null;
  return (
    <div className="likes-character-passive">
      <strong>
        {t('角色常驻被动', 'Always-active character passive')} ·{' '}
        {onInspect ? (
          <button type="button" className="likes-term" onClick={() => onInspect(role.passive!.id)}>
            {content.name}
          </button>
        ) : (
          content.name
        )}
      </strong>
      <span>{content.description}</span>
    </div>
  );
}
