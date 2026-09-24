import { decoded, idempotentOptions } from './api';
import { charityScopePath } from './charityScope';
import { normalizeKeySummary } from './donationPages';
import { decimalID, invalidResponse, record } from './wire';

function path(modelID: string, donationID: string, keyID: string) {
  return charityScopePath(
    `/api/steward/donations/${decimalID(donationID, 'donation id')}/keys/${decimalID(keyID, 'key id')}`,
    modelID,
  );
}

export function getScopedDonationKey(
  modelID: string,
  donationID: string,
  keyID: string,
  signal?: AbortSignal,
) {
  return decoded(
    path(modelID, donationID, keyID),
    (value) => {
      const result = normalizeKeySummary(value, 0);
      if (result.donation_id !== donationID || result.key_id !== keyID)
        invalidResponse('donation key identity');
      return result;
    },
    { signal },
  );
}

export function patchScopedDonationKey(
  modelID: string,
  donationID: string,
  keyID: string,
  input: unknown,
  key: string,
) {
  return decoded(
    path(modelID, donationID, keyID),
    (value) => {
      const root = record(
        value,
        ['donation_id', 'key_id', 'donation_revision', 'key'],
        'donation key receipt',
      );
      const result = normalizeKeySummary(root.key, 0);
      if (
        root.donation_id !== donationID ||
        root.key_id !== keyID ||
        result.donation_id !== donationID ||
        result.key_id !== keyID ||
        root.donation_revision !== result.donation_revision
      )
        invalidResponse('donation key receipt identity');
      return result;
    },
    idempotentOptions(key, { method: 'PATCH', json: input }),
  );
}
