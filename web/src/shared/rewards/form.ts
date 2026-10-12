import { timeDraftValue, type TimeDraft } from '@shared/time';
export function validRewardWindow(quota: string, start: TimeDraft, end: TimeDraft): boolean {
  if (quota !== '' && (!/^[1-9][0-9]*$/.test(quota) || !Number.isSafeInteger(Number(quota))))
    return false;
  const from = timeDraftValue(start),
    to = timeDraftValue(end);
  return from !== undefined && to !== undefined && (from == null || to == null || from < to);
}
