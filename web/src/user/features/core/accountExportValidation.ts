import { ApiError } from '@shared/query/http';

/** Validate the attachment timestamp and account identity. */
export function validateAccountExportIdentity(
  record: Record<string, unknown>,
  accountId: string,
): void {
  const user = record.user;
  if (
    user === null ||
    typeof user !== 'object' ||
    Array.isArray(user) ||
    (user as Record<string, unknown>).id !== accountId ||
    !Number.isSafeInteger(record.generated_at) ||
    (record.generated_at as number) < 0 ||
    (record.generated_at as number) > 253_402_300_799
  ) {
    throw new ApiError('invalid_response', 'The server returned an invalid account export.', 200);
  }
}
