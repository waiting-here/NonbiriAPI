import { decoded, queryPath } from './api';
import { normalizeDonationHandling } from './charity';
import { normalizePageMetadata, validatePageResponse, type PageSize } from './pageNumbers';
import { array, decimal, decimalID, invalidResponse, record, unixSecond } from './wire';

export function getDonationHandlingPage(
  handling: 'pending' | 'processed' | 'all',
  donationID: string,
  page: string,
  pageSize: PageSize,
  signal?: AbortSignal,
) {
  return decoded(
    queryPath('/api/steward/donation-handling', {
      handling,
      donation_id: donationID || undefined,
      page,
      page_size: pageSize,
    }),
    (value) => {
      const root = record(value, ['data', 'pagination', 'next_cursor'], 'donation handling page');
      if (root.next_cursor !== null) invalidResponse('donation handling cursor');
      const pagination = normalizePageMetadata(root.pagination);
      validatePageResponse(pagination, page, pageSize);
      const data = array(root.data, 'handling summaries', pageSize).map((value) => {
        const row = record(
          value,
          ['donation_id', 'created_at', 'key_count', 'bound_key_count', 'handling'],
          'handling summary',
        );
        const result = {
          donation_id: decimalID(row.donation_id, 'donation id'),
          created_at: unixSecond(row.created_at, 'creation time'),
          key_count: decimal(row.key_count, 'key count'),
          bound_key_count: decimal(row.bound_key_count, 'bound key count'),
          handling: normalizeDonationHandling(row.handling),
        };
        if (
          BigInt(result.bound_key_count) > BigInt(result.key_count) ||
          (donationID && result.donation_id !== donationID) ||
          (handling !== 'all' && result.handling.state !== handling)
        )
          invalidResponse('handling summary filters');
        return result;
      });
      return { data, pagination };
    },
    { signal },
  );
}
