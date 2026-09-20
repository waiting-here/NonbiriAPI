import { array, invalidResponse, nullableUnixSecond, oneOf, record, string, unixSecond } from './wire';

export function automaticRestrictions(value: unknown) {
  const items = array(value, 'automatic restrictions', 2).map((value) => {
    const item = record(value, ['kind', 'reason_code', 'reason', 'started_at', 'ends_at'], 'automatic restriction');
    const startedAt = unixSecond(item.started_at, 'restriction start');
    const endsAt = nullableUnixSecond(item.ends_at, 'restriction end');
    if (endsAt !== null && endsAt < startedAt) invalidResponse('restriction time');
    return {
      kind: oneOf(item.kind, ['ban', 'charity_suspend'] as const, 'restriction kind'),
      reason_code: oneOf(item.reason_code, ['charity_rpm', 'charity_short_content'] as const, 'restriction reason code'),
      reason: string(item.reason, 'restriction reason', { min: 1, max: 256, bytes: 1024 }),
      started_at: startedAt,
      ends_at: endsAt,
    };
  });
  if (new Set(items.map((item) => item.kind)).size !== items.length) invalidResponse('duplicate restriction');
  return items;
}

export type AutomaticRestriction = ReturnType<typeof automaticRestrictions>[number];
