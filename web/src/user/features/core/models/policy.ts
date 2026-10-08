import type { RolePolicy } from '@shared/rolePolicy';

export function sameRolePolicy(current: RolePolicy | undefined, expected: RolePolicy): boolean {
  const policy = current ?? { default_action: 'native', rules: {} };
  return (
    policy.default_action === expected.default_action &&
    Object.keys(policy.rules).length === Object.keys(expected.rules).length &&
    Object.entries(expected.rules).every(([role, action]) => policy.rules[role] === action)
  );
}
