export const stateLabels: Record<string, string> = {
  pending: 'common.agent.states.pending',
  running: 'common.agent.states.running',
  waiting_confirmation: 'common.agent.states.waiting_confirmation',
  completed: 'common.agent.states.completed',
  approved: 'common.agent.states.approved',
  succeeded: 'common.agent.states.succeeded',
  failed: 'common.agent.states.failed',
  cancelled: 'common.agent.states.cancelled',
  interrupted: 'common.agent.states.interrupted',
  rejected: 'common.agent.states.rejected',
  invalidated: 'common.agent.states.invalidated',
  superseded: 'common.agent.states.invalidated',
};
export const sourceLabels: Record<string, string> = {
  auto_review: 'common.agent.sources.auto_review',
  admin_chat: 'common.agent.sources.admin_chat',
  steward_chat: 'common.agent.sources.steward_chat',
};
export const roleLabels: Record<string, string> = {
  user: 'common.agent.roles.user',
  assistant: 'common.agent.roles.assistant',
  system: 'common.agent.roles.system',
  tool: 'common.agent.roles.tool',
  admin: 'common.agent.roles.admin',
  level5: 'common.agent.roles.steward',
  level6: 'common.agent.roles.steward',
};
export function compactInstructions(content: string): string | null {
  const match = /^\/compact(?:\s+([\s\S]*))?$/u.exec(content.trim());
  return match ? (match[1]?.trim() ?? '') : null;
}
export function textDiff(before: string, after: string) {
  const old = before.split('\n'),
    next = after.split('\n');
  let first = 0,
    last = 0;
  while (first < old.length && first < next.length && old[first] === next[first]) first++;
  while (
    last < old.length - first &&
    last < next.length - first &&
    old[old.length - 1 - last] === next[next.length - 1 - last]
  )
    last++;
  const a = old.slice(first, old.length - last);
  const b = next.slice(first, next.length - last);
  const changes: { kind: 'same' | 'remove' | 'add'; text: string }[] = [];
  // Bound the matrix for large memory replacements; unchanged edges are always retained.
  if (a.length && b.length && (a.length + 1) * (b.length + 1) <= 160_000) {
    const width = b.length + 1;
    const lengths = new Uint16Array((a.length + 1) * width);
    for (let i = a.length - 1; i >= 0; i--)
      for (let j = b.length - 1; j >= 0; j--)
        lengths[i * width + j] =
          a[i] === b[j]
            ? lengths[(i + 1) * width + j + 1]! + 1
            : Math.max(lengths[(i + 1) * width + j]!, lengths[i * width + j + 1]!);
    let i = 0,
      j = 0;
    while (i < a.length || j < b.length) {
      if (i < a.length && j < b.length && a[i] === b[j]) {
        changes.push({ kind: 'same', text: a[i++]! });
        j++;
      } else if (
        i < a.length &&
        (j === b.length || lengths[(i + 1) * width + j]! >= lengths[i * width + j + 1]!)
      )
        changes.push({ kind: 'remove', text: a[i++]! });
      else changes.push({ kind: 'add', text: b[j++]! });
    }
  } else {
    // Large replacements use unique matching lines as anchors without allocating a quadratic table.
    const positions = new Map<string, number>();
    b.forEach((line, index) => positions.set(line, positions.has(line) ? -1 : index));
    let left = 0,
      right = 0;
    for (let i = 0; i < a.length; i++) {
      const match = positions.get(a[i]!);
      if (match === undefined || match < right) continue;
      while (left < i) changes.push({ kind: 'remove', text: a[left++]! });
      while (right < match) changes.push({ kind: 'add', text: b[right++]! });
      while (left < a.length && right < b.length && a[left] === b[right]) {
        changes.push({ kind: 'same', text: a[left++]! });
        right++;
      }
      i = left - 1;
    }
    while (left < a.length) changes.push({ kind: 'remove', text: a[left++]! });
    while (right < b.length) changes.push({ kind: 'add', text: b[right++]! });
  }
  return [
    ...old.slice(0, first).map((text) => ({ kind: 'same' as const, text })),
    ...changes,
    ...old.slice(old.length - last).map((text) => ({ kind: 'same' as const, text })),
  ];
}
