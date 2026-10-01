import { useFatFishText } from './copy';
import type { Condition, NodeRecord } from './api';

type Kind = 'none' | 'all' | 'any' | 'passed' | 'stars' | 'passed_count' | 'total_stars';
const kinds: Kind[] = ['none', 'all', 'any', 'passed', 'stars', 'passed_count', 'total_stars'];
export function conditionKind(value: Condition): Kind {
  const key = Object.keys(value)[0];
  return kinds.includes(key as Kind) ? key as Kind : 'none';
}
function create(kind: Kind, nodes: NodeRecord[]): Condition {
  switch (kind) {
    case 'none': return {};
    case 'all': return { all: [{}] };
    case 'any': return { any: [{}] };
    case 'passed': return { passed: nodes[0]?.id ?? '' };
    case 'stars': return { stars: { node: nodes[0]?.id ?? '', min: 1 } };
    case 'passed_count': return { passed_count: 1 };
    case 'total_stars': return { total_stars: 1 };
  }
}
export function conditionReferences(value: Condition): string[] {
  if ('all' in value) return value.all.flatMap(conditionReferences);
  if ('any' in value) return value.any.flatMap(conditionReferences);
  if ('passed' in value) return [value.passed];
  if ('stars' in value) return [value.stars.node];
  return [];
}
export function validateCondition(value: Condition, ids: Set<string>, depth = 1, budget = { count: 0 }): string | null {
  if (depth === 1 && new TextEncoder().encode(JSON.stringify(value)).byteLength > 32768) return 'Condition exceeds 32 KiB';
  budget.count++;
  if (depth > 8 || budget.count > 128) return 'Condition exceeds depth 8 or 128 entries';
  if ('all' in value || 'any' in value) {
    const children = 'all' in value ? value.all : value.any;
    if (!children.length) return 'All/any group needs at least one child';
    for (const child of children) { const error = validateCondition(child, ids, depth + 1, budget); if (error) return error; }
  } else if ('passed' in value && !ids.has(value.passed)) return 'Condition references an unknown node';
  else if ('stars' in value && (!ids.has(value.stars.node) || !Number.isInteger(value.stars.min) || value.stars.min < 1 || value.stars.min > 3)) return 'Star prerequisite is invalid';
  else if ('passed_count' in value && (!Number.isSafeInteger(value.passed_count) || value.passed_count < 0)) return 'Passed count is invalid';
  else if ('total_stars' in value && (!Number.isSafeInteger(value.total_stars) || value.total_stars < 0)) return 'Total stars is invalid';
  return null;
}
export function ConditionEditor({ value, nodes, currentID, onChange, depth = 1 }: {
  value: Condition; nodes: NodeRecord[]; currentID?: string; onChange(value: Condition): void; depth?: number;
}) {
  const text = useFatFishText(), kind = conditionKind(value);
  const choices = nodes;
  const nodeOptions = <>{choices.map((node) => <option key={node.id} value={node.id}>{node.order + 1}. {node.title}{node.id === currentID ? text('this_node') : ''}</option>)}</>;
  return <div className="fatfish-condition">
    <label>{text('condition')}<select value={kind} onChange={(event) => onChange(create(event.target.value as Kind, choices))}>
      <option value="none">{text('always')}</option>
      <option value="all">{text('all_of')}</option><option value="any">{text('any_of')}</option>
      <option value="passed">{text('passed_node')}</option><option value="stars">{text('node_stars')}</option>
      <option value="passed_count">{text('passed_count')}</option><option value="total_stars">{text('total_stars')}</option>
    </select></label>
    {('all' in value || 'any' in value) ? <div className="fatfish-condition-children">
      {(kind === 'all' ? (value as { all: Condition[] }).all : (value as { any: Condition[] }).any).map((child, index, children) => <div key={index}>
        <ConditionEditor value={child} nodes={nodes} currentID={currentID} depth={depth + 1} onChange={(next) => {
          const changed = [...children]; changed[index] = next; onChange(kind === 'all' ? { all: changed } : { any: changed });
        }} />
        <button type="button" disabled={children.length <= 1} onClick={() => onChange(kind === 'all' ? { all: children.filter((_, childIndex) => childIndex !== index) } : { any: children.filter((_, childIndex) => childIndex !== index) })}>{text('remove_child')}</button>
      </div>)}
      <button type="button" disabled={depth >= 8} onClick={() => onChange(kind === 'all' ? { all: [...(value as { all: Condition[] }).all, {}] } : { any: [...(value as { any: Condition[] }).any, {}] })}>{text('add_child')}</button>
    </div> : null}
    {'passed' in value ? <label>{text('node')}<select value={value.passed} onChange={(event) => onChange({ passed: event.target.value })}>{nodeOptions}</select></label> : null}
    {'stars' in value ? <><label>{text('node')}<select value={value.stars.node} onChange={(event) => onChange({ stars: { ...value.stars, node: event.target.value } })}>{nodeOptions}</select></label>
      <label>{text('minimum_stars')}<input type="number" min={1} max={3} value={value.stars.min} onChange={(event) => onChange({ stars: { ...value.stars, min: Number(event.target.value) } })} /></label></> : null}
    {'passed_count' in value ? <label>{text('minimum_passed_count')}<input type="number" min={0} max={128} value={value.passed_count} onChange={(event) => onChange({ passed_count: Number(event.target.value) })} /></label> : null}
    {'total_stars' in value ? <label>{text('minimum_total_stars')}<input type="number" min={0} max={384} value={value.total_stars} onChange={(event) => onChange({ total_stars: Number(event.target.value) })} /></label> : null}
  </div>;
}
