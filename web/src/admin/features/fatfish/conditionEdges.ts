import type { Condition } from './api';

export type ConditionPath = Array<{ group: 'all' | 'any'; index: number }>;
export interface ConditionEdge { source: string; path: ConditionPath; kind: 'passed' | 'stars'; min?: number }
export function conditionEdges(value: Condition, path: ConditionPath = []): ConditionEdge[] {
  if ('all' in value) return value.all.flatMap((child, index) => conditionEdges(child, [...path, { group: 'all', index }]));
  if ('any' in value) return value.any.flatMap((child, index) => conditionEdges(child, [...path, { group: 'any', index }]));
  if ('passed' in value) return [{ source: value.passed, path, kind: 'passed' }];
  if ('stars' in value) return [{ source: value.stars.node, path, kind: 'stars', min: value.stars.min }];
  return [];
}
export function addPassedPrerequisite(value: Condition, source: string): Condition {
  if (conditionEdges(value).some((edge) => edge.kind === 'passed' && edge.source === source)) return value;
  if (!Object.keys(value).length) return { passed: source };
  if ('all' in value) return { all: [...value.all, { passed: source }] };
  return { all: [value, { passed: source }] };
}
export class EmptyConditionGroup extends Error {}
export function removeConditionEdge(value: Condition, path: ConditionPath,
  emptyChoice?: 'remove_group' | 'always'): Condition {
  const remove = (current: Condition, remaining: ConditionPath): Condition | null => {
    if (!remaining.length) {
      if (!('passed' in current) && !('stars' in current)) throw new Error('The selected condition leaf changed.');
      return null;
    }
    const [{ group, index }, ...rest] = remaining;
    const children = group === 'all' && 'all' in current ? current.all : group === 'any' && 'any' in current ? current.any : null;
    if (!children || !children[index]) throw new Error('The selected condition path changed.');
    const changed = remove(children[index], rest);
    const next = children.flatMap((child, at) => at !== index ? [child] : changed === null ? [] : [changed]);
    if (!next.length) {
      if (!emptyChoice) throw new EmptyConditionGroup('Removing this leaf would empty a group.');
      return emptyChoice === 'always' ? {} : null;
    }
    return group === 'all' ? { all: next } : { any: next };
  };
  return remove(value, path) ?? {};
}
export function conditionBadges(value: Condition): Array<{ kind: 'all_of' | 'any_of' | 'passed_count' | 'total_stars'; min?: number }> {
  if ('all' in value) return [{ kind: 'all_of' }, ...value.all.flatMap(conditionBadges)];
  if ('any' in value) return [{ kind: 'any_of' }, ...value.any.flatMap(conditionBadges)];
  if ('passed_count' in value) return [{ kind: 'passed_count', min: value.passed_count }];
  if ('total_stars' in value) return [{ kind: 'total_stars', min: value.total_stars }];
  return [];
}
