import { ApiError } from '@shared/query/http';
import { decoded, idempotentOptions } from './api';
import { boolean, decimal, decimalID, invalidResponse, record } from './wire';

export function validFailureThreshold(value: string): boolean {
  return /^(0|[1-9][0-9]{0,38})$/.test(value) && BigInt(value) <= (1n << 128n) - 1n;
}
export interface FailurePolicy {
  donation_id: string;
  donation_key_id: string;
  failure_disable_threshold: string;
  failure_streak: string;
  failure_disabled: boolean;
  revision: string;
}
export function saveFailurePolicy(
  role: 'owner' | 'admin' | 'steward',
  donationID: string,
  keyID: string,
  input: { expected_revision: string; failure_disable_threshold: string },
  idempotencyKey: string,
): Promise<FailurePolicy> {
  if (!validFailureThreshold(input.failure_disable_threshold))
    throw new ApiError('invalid_request', 'Invalid failure threshold.', 400);
  const base = role === 'admin' ? '/admin/api' : role === 'steward' ? '/api/steward' : '/api';
  return decoded(
    `${base}/donations/${decimalID(donationID, 'donation id')}/keys/${decimalID(keyID, 'key id')}/failure-policy`,
    (value) => {
      const fields = record(
        value,
        [
          'donation_id',
          'donation_key_id',
          'failure_disable_threshold',
          'failure_streak',
          'failure_disabled',
          'revision',
        ],
        'failure policy',
      );
      if (fields.donation_id !== donationID || fields.donation_key_id !== keyID)
        invalidResponse('failure policy identity');
      return {
        donation_id: donationID,
        donation_key_id: keyID,
        failure_disable_threshold: decimal(fields.failure_disable_threshold, 'failure threshold'),
        failure_streak: decimal(fields.failure_streak, 'failure count'),
        failure_disabled: boolean(fields.failure_disabled, 'failure disabled'),
        revision: decimalID(fields.revision, 'donation revision'),
      };
    },
    idempotentOptions(idempotencyKey, { method: 'PATCH', json: input }),
  );
}
