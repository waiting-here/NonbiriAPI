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
  return [
    ...old.slice(0, first).map((text) => ({ kind: 'same', text })),
    ...old.slice(first, old.length - last).map((text) => ({ kind: 'remove', text })),
    ...next.slice(first, next.length - last).map((text) => ({ kind: 'add', text })),
    ...old.slice(old.length - last).map((text) => ({ kind: 'same', text })),
  ];
}
