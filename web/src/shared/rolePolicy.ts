import { invalidResponse, oneOf, record } from '@shared/operations/wire';

export const roleActions = [
  'native',
  'passthrough',
  'system',
  'user',
  'assistant',
  'reject',
] as const;
export type RoleAction = (typeof roleActions)[number];

export interface RolePolicy {
  default_action: RoleAction;
  rules: Record<string, RoleAction>;
}

export interface RolePolicyDraft {
  defaultAction: RoleAction;
  rules: { role: string; action: RoleAction }[];
}

export type RolePolicyFieldError = {
  kind: 'name' | 'reserved' | 'duplicate' | 'count';
  row?: number;
};

export function draftFromRolePolicy(policy?: RolePolicy): RolePolicyDraft {
  return {
    defaultAction: policy?.default_action ?? 'native',
    rules: Object.entries(policy?.rules ?? {}).map(([role, action]) => ({ role, action })),
  };
}

export function normalizeRolePolicy(value: unknown, label = 'role policy'): RolePolicy {
  if (value === undefined) return { default_action: 'native', rules: {} };
  const root = record(value, ['default_action', 'rules'], label);
  if (root.rules === null || typeof root.rules !== 'object' || Array.isArray(root.rules))
    invalidResponse(`${label} rules`);
  return {
    default_action: oneOf(root.default_action, roleActions, `${label} default action`),
    rules: Object.fromEntries(
      Object.entries(root.rules).map(([role, action]) => [
        role,
        oneOf(action, roleActions, `${label} role action`),
      ]),
    ),
  };
}

export function buildRolePolicy(
  draft: RolePolicyDraft,
): { policy: RolePolicy; error?: never } | { policy?: never; error: RolePolicyFieldError } {
  if (draft.rules.length > 32) return { error: { kind: 'count' } };
  const seen = new Set<string>();
  const reserved = new Set(['system', 'user', 'assistant', 'tool', 'function']);
  for (const [row, rule] of draft.rules.entries()) {
    if (
      !rule.role ||
      rule.role.trim() !== rule.role ||
      Array.from(rule.role).length > 64 ||
      /\p{Cc}/u.test(rule.role)
    ) {
      return { error: { kind: 'name', row } };
    }
    if (reserved.has(rule.role)) return { error: { kind: 'reserved', row } };
    if (seen.has(rule.role)) return { error: { kind: 'duplicate', row } };
    seen.add(rule.role);
  }
  return {
    policy: {
      default_action: draft.defaultAction,
      rules: Object.fromEntries(draft.rules.map(({ role, action }) => [role, action])),
    },
  };
}
