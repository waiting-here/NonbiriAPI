import type { ModeCatalog } from './catalog';
import type { Selection } from './types';

export function initialSelection(c: ModeCatalog): Selection {
  return {
    role: c.roles[0].id,
    harness: null,
    skills: [...c.loadouts.find((p) => p.role === c.roles[0].id)!.skills],
  };
}
export function selectionProblem(c: ModeCatalog, s: Selection): 'slots' | 'sustainable' | null {
  const slots = 4 + (c.harnesses.find((h) => h.id === s.harness)?.activeSlots ?? 0);
  if (
    s.skills.length < 1 ||
    s.skills.length > slots ||
    new Set(s.skills).size !== s.skills.length ||
    s.skills.some(
      (id) =>
        !c.skills.some((sk) => sk.id === id && (sk.owner === s.role || sk.owner === '全局公共')),
    )
  )
    return 'slots';
  if (
    !s.skills.some((id) => {
      const sk = c.skills.find((x) => x.id === id);
      return (
        sk?.stable &&
        sk.id !== 'PUB41' &&
        sk.maxUses === null &&
        sk.effects.base.likes > 0 &&
        (sk.payment !== 'sub' || s.role !== 'DeepSeek')
      );
    })
  )
    return 'sustainable';
  return null;
}
