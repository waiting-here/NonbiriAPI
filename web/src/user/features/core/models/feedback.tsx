import { OutcomeNote } from '@shared/components/ui';
import { isForbidden, isUnauthorized } from '@shared/query/http';
import { MutationNotice } from '../components';

export type VisibleOutcome = 'conflict' | 'unknown' | 'error' | null;
export type PermissionLoss = { scope: string; error: unknown };

export function asNotice(
  outcome: VisibleOutcome,
  onCheck: () => void,
  busy: boolean,
  savedRefreshFailed = false,
) {
  return savedRefreshFailed ? (
    <OutcomeNote busy={busy} outcome={{ kind: 'savedRefreshFailed', recheck: onCheck }} />
  ) : (
    <MutationNotice outcome={outcome} onCheck={onCheck} busy={busy} />
  );
}

export function isAccessLoss(error: unknown): boolean {
  return isUnauthorized(error) || isForbidden(error);
}
