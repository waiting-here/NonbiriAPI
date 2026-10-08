import {
  containsForbiddenControl as hasForbiddenControl,
  normalizePublicDescription,
  type CharityRole,
  type CharityState,
  type DonationStatus,
  type TokenPrices,
} from '@shared/operations/charity';
import { managementResourceID } from '@shared/operations/charityModelPages';
import '@shared/operations/operations.css';
import { type PageSize } from '@shared/operations/pageNumbers';
import { amount } from '@shared/operations/wire';
import '@shared/styles/charity-management.css';

export function snapshotPage<T>(items: readonly T[], requestedPage: string, pageSize: PageSize) {
  const totalPages = Math.max(1, Math.ceil(items.length / pageSize));
  const page = Math.min(Number(requestedPage), totalPages);
  const offset = (page - 1) * pageSize;
  return {
    data: items.slice(offset, offset + pageSize),
    offset,
    pagination: {
      page: String(page),
      page_size: pageSize,
      total_items: String(items.length),
      total_pages: String(totalPages),
    },
  };
}

export function oneParam(params: URLSearchParams, name: string): string {
  const values = params.getAll(name);
  return values.length === 1 ? values[0] : '';
}

export function selectedID(params: URLSearchParams, name: string): string {
  const value = oneParam(params, name);
  return managementResourceID(value) ? value : '';
}

export function samePageFamily(
  previous: readonly unknown[] | undefined,
  current: readonly unknown[],
): boolean {
  return (
    previous?.length === current.length &&
    current.slice(0, -2).every((part, index) => part === previous[index])
  );
}

export const charityCopyKey = (role: CharityRole, key: string) =>
  `${role === 'admin' ? 'admin.charity' : 'user.steward'}.${key}`;
export const charityStatusKey = (role: CharityRole, status: DonationStatus) =>
  charityCopyKey(role, `status.${status}`);
export const charityStateKey = (state: CharityState) =>
  `common.operations.charity.charityState.${state}`;
export const reviewerRoleKey = (role: 'admin' | 'steward') =>
  `common.operations.charity.role.${role}`;
export const tokenPriceKeyPart: Record<keyof TokenPrices, string> = {
  uncached_input: 'uncached',
  cache_write_input: 'cache_write',
  cache_read_input: 'cache_read',
  output: 'output',
};
export const tokenPriceCopyKey = (
  role: CharityRole,
  side: 'userPrices' | 'donorRewards',
  field: keyof TokenPrices,
) =>
  charityCopyKey(
    role,
    `${tokenPriceKeyPart[field]}_${side === 'userPrices' ? 'user_price' : 'donor_reward'}_milli`,
  );

export const MAX_MONEY_MILLI = 9_000_000_000_000_000n;
export const MAX_TOKEN_RESERVE = 2_147_483_647;
export const MAX_UNIX_SECOND = 253_402_300_799;
export const CANONICAL_DECIMAL = /^(0|[1-9][0-9]*)$/;
export const MODEL_LEVELS = [1, 2, 3, 4, 5, 6] as const;

export function validText(value: string, maximum: number, required = false): boolean {
  return (
    (!required || value.trim().length > 0) &&
    Array.from(value).length <= maximum &&
    !hasForbiddenControl(value)
  );
}

export function normalizePublicDescriptionInput(value: string): string {
  return value.replace(/\r\n/g, '\n');
}

export function validPublicDescription(value: string): boolean {
  try {
    normalizePublicDescription(value, 'description');
    return true;
  } catch {
    return false;
  }
}

export function validCount(value: string | null): boolean {
  if (value === null) return true;
  if (!CANONICAL_DECIMAL.test(value)) return false;
  try {
    return BigInt(value) <= MAX_MONEY_MILLI;
  } catch {
    return false;
  }
}

export function validAmount(value: string | null): boolean {
  if (value === null) return true;
  try {
    amount(value, 'credits', false, MAX_MONEY_MILLI);
    return true;
  } catch {
    return false;
  }
}

export function validTokenReserveCredits(value: string | null): boolean {
  return value === null || (value !== '0' && validAmount(value));
}

export function validTokenReserve(value: number): boolean {
  return Number.isSafeInteger(value) && value >= 0 && value <= MAX_TOKEN_RESERVE;
}
