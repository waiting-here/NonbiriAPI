import { decoded, idempotentOptions } from './api';
import { charityScopePath } from './charityScope';
import {
  array,
  decimal,
  decimalID,
  invalidResponse,
  nullableUnixSecond,
  oneOf,
  record,
  string,
} from './wire';
import type { CharityRole } from './charity';

export interface DiscoveryRef {
  donation_id: string;
  key_id: string;
}
export type DiscoveryTarget = DiscoveryRef | { donation_id: string | null };
export interface DiscoverySelection {
  items: DiscoveryRef[];
  next_cursor: string | null;
}
export interface DiscoveryEvidence {
  state: 'unknown' | 'checking' | 'succeeded' | 'failed';
  revision: string;
  result: 'empty' | 'nonempty' | null;
  safe_class: 'none' | 'auth' | 'rate_limit' | 'timeout' | 'protocol' | 'transport' | 'interrupted';
  observed_at: number | null;
  count: string | null;
}

const base = (role: CharityRole) => (role === 'admin' ? '/admin/api' : '/api/steward');
const path = (role: CharityRole, item: DiscoveryRef) =>
  `${base(role)}/donations/${decimalID(item.donation_id, 'donation id')}/keys/${decimalID(item.key_id, 'donation key id')}/models`;

export function normalizeManagedDiscovery(value: unknown): DiscoveryEvidence {
  const body = record(
    value,
    ['state', 'revision', 'result', 'safe_class', 'observed_at', 'count'],
    'model discovery',
  );
  const evidence: DiscoveryEvidence = {
    state: oneOf(body.state, ['unknown', 'checking', 'succeeded', 'failed'], 'discovery state'),
    revision: decimalID(body.revision, 'discovery revision'),
    result:
      body.result === null ? null : oneOf(body.result, ['empty', 'nonempty'], 'discovery result'),
    safe_class: oneOf(
      body.safe_class,
      ['none', 'auth', 'rate_limit', 'timeout', 'protocol', 'transport', 'interrupted'],
      'discovery failure',
    ),
    observed_at: nullableUnixSecond(body.observed_at, 'discovery time'),
    count: body.count === null ? null : decimal(body.count, 'discovery count'),
  };
  if (evidence.state === 'succeeded') {
    if (
      evidence.count === null ||
      BigInt(evidence.count) > 1000n ||
      evidence.observed_at === null ||
      evidence.safe_class !== 'none' ||
      evidence.result !== (evidence.count === '0' ? 'empty' : 'nonempty')
    )
      invalidResponse('successful discovery');
  } else if (
    evidence.count !== null ||
    evidence.result !== null ||
    (evidence.state === 'failed'
      ? evidence.safe_class === 'none'
      : evidence.safe_class !== 'none') ||
    (evidence.state === 'unknown' ? evidence.observed_at !== null : evidence.observed_at === null)
  )
    invalidResponse('discovery state');
  return evidence;
}

export function selectDonationDiscoveries(
  role: CharityRole,
  donationID: string | null,
  cursor: string | null,
  modelID?: string,
): Promise<DiscoverySelection> {
  if (donationID !== null) decimalID(donationID, 'donation id');
  return decoded(
    charityScopePath(`${base(role)}/donation-keys/models/refresh/selection`, modelID),
    (value) => {
      const body = record(value, ['items', 'next_cursor'], 'discovery selection');
      const items = array(body.items, 'discovery references', 100).map((value) => {
        const row = record(value, ['donation_id', 'key_id'], 'discovery reference');
        return {
          donation_id: decimalID(row.donation_id, 'donation id'),
          key_id: decimalID(row.key_id, 'donation key id'),
        };
      });
      if (
        new Set(items.map((item) => item.key_id)).size !== items.length ||
        items.some((item) => donationID !== null && item.donation_id !== donationID)
      )
        invalidResponse('discovery selection identity');
      return {
        items,
        next_cursor:
          body.next_cursor === null
            ? null
            : string(body.next_cursor, 'discovery cursor', { min: 1, max: 4096, bytes: 4096 }),
      };
    },
    {
      method: 'POST',
      json: { donation_id: donationID, cursor },
      signal: AbortSignal.timeout(10000),
    },
  );
}

export function startDonationDiscovery(
  role: CharityRole,
  item: DiscoveryRef,
  key: string,
  modelID?: string,
): Promise<DiscoveryEvidence> {
  return decoded(
    charityScopePath(`${path(role, item)}/refresh`, modelID),
    (value) => {
      const body = record(value, ['operation_id', 'evidence'], 'accepted discovery');
      if (!/^op_[A-Za-z0-9_-]{21}[AQgw]$/.test(string(body.operation_id, 'operation id')))
        invalidResponse('operation id');
      const evidence = normalizeManagedDiscovery(body.evidence);
      if (evidence.state !== 'checking') invalidResponse('accepted discovery state');
      return evidence;
    },
    idempotentOptions(key, { method: 'POST', signal: AbortSignal.timeout(10000) }),
  );
}

export function readDonationDiscovery(
  role: CharityRole,
  item: DiscoveryRef,
  modelID?: string,
): Promise<DiscoveryEvidence> {
  return decoded(
    charityScopePath(`${path(role, item)}/discovery`, modelID),
    normalizeManagedDiscovery,
    {
      signal: AbortSignal.timeout(10000),
    },
  );
}
