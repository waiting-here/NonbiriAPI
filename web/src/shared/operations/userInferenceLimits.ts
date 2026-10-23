import type { AdminUser, ManagementRole } from './managedUsers';

export const INFERENCE_LIMITS = [
  { key: 'global_rpm_limit', label: 'management.users.globalRPMLimit', max: 4096 },
  { key: 'rpm_limit', label: 'management.users.rpmLimit', max: 4096 },
  { key: 'light_rpm_limit', label: 'management.users.lightRPMLimit', max: 4096 },
  { key: 'concurrency_limit', label: 'management.users.concurrencyLimit', max: 100000 },
  { key: 'light_concurrency_limit', label: 'management.users.lightConcurrencyLimit', max: 100000 },
] as const;
export type InferenceLimitKey = (typeof INFERENCE_LIMITS)[number]['key'];
export type InferenceLimitDraft = Record<InferenceLimitKey, string>;
export type InferenceLimitPatch = Partial<Record<InferenceLimitKey, string | null>>;

export function canEditInferenceLimits(
  user: AdminUser,
  role: ManagementRole,
  account: string,
  trainee: boolean,
) {
  return (
    role === 'admin' ||
    (!user.is_admin && user.id !== account && user.level.effective < (trainee ? 5 : 6))
  );
}
export function inferenceLimitDraft(user: AdminUser): InferenceLimitDraft {
  return Object.fromEntries(
    INFERENCE_LIMITS.map(({ key }) => [key, user[key] ?? '']),
  ) as InferenceLimitDraft;
}
export function parseInferenceLimit(value: string, max: number): string | null | undefined {
  if (!value.trim()) return null;
  if (!/^[1-9][0-9]*$/.test(value)) return undefined;
  const n = Number(value);
  return Number.isSafeInteger(n) && n <= max ? value : undefined;
}
