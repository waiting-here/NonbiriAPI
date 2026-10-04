import { decoded, queryPath } from './api';
import { automaticRestrictions, type AutomaticRestriction } from './restrictions';
import { normalizeAutomaticReason, type AutomaticReason } from './automaticReason';
import { array, boolean, nullableUnixSecond, oneOf, record, string, unixSecond } from './wire';

export interface DenialReason {
  kind: 'ban' | 'blacklist' | 'blacklist_note';
  reason: string;
  started_at: number;
  ends_at: number | null;
  automatic?: AutomaticRestriction;
  automatic_reason: AutomaticReason | null;
  reason_codes: string[];
}
export function getAccessDenial(cursor?: string, signal?: AbortSignal) {
  return decoded(
    queryPath('/api/auth/access-denied-reasons', { cursor }),
    (value) => {
      const root = record(value, ['restricted', 'items', 'next_cursor'], 'access denial', [
        'restricted',
        'items',
      ]);
      return {
        restricted: boolean(root.restricted, 'access restricted'),
        next_cursor:
          root.next_cursor === undefined
            ? undefined
            : string(root.next_cursor, 'denial cursor', { min: 1, ascii: true }),
        items: array(root.items, 'denial reasons', 20).map((value): DenialReason => {
          const item = record(
            value,
            [
              'kind',
              'reason',
              'started_at',
              'ends_at',
              'automatic',
              'automatic_reason',
              'reason_codes',
            ],
            'denial reason',
            ['kind', 'reason', 'started_at', 'ends_at'],
          );
          return {
            kind: oneOf(
              item.kind,
              ['ban', 'blacklist', 'blacklist_note'] as const,
              'denial reason kind',
            ),
            reason: string(item.reason, 'denial reason text', { multiline: true }),
            started_at: unixSecond(item.started_at, 'denial start'),
            ends_at: nullableUnixSecond(item.ends_at, 'denial end'),
            automatic:
              item.automatic === undefined ? undefined : automaticRestrictions([item.automatic])[0],
            automatic_reason: normalizeAutomaticReason(item.automatic_reason),
            reason_codes: array(item.reason_codes ?? [], 'reason codes', 32).map((code) =>
              string(code, 'reason code', { ascii: true }),
            ),
          };
        }),
      };
    },
    { signal },
  );
}
