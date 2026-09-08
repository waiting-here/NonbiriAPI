import { ApiError } from '@shared/query/http';
import { decoded, queryPath } from './api';
import { array, decimalID, invalidResponse, oneOf, record } from './wire';
import {
  isPageNumber,
  isPageSize,
  normalizePageMetadata,
  validatePageResponse,
  type PageMetadata,
  type PageSize,
} from './pageNumbers';
import {
  normalizeCharityBindingCandidate,
  type CharityBindingCandidate,
  type CharityRole,
} from './charity';
import { managementResourceID, validManagementSearch } from './charityModelPages';

export interface CharityBindingCandidatePageFilters {
  donation_id?: string;
  donation_key_id?: string;
  source?: '' | 'automatic' | 'manual';
  q?: string;
}

/** Alias kept descriptive for callers that name the response a managed page. */
export type ManagedBindingCandidatePageFilters = CharityBindingCandidatePageFilters;

export interface CharityBindingCandidatePage {
  data: CharityBindingCandidate[];
  next_cursor: null;
  pagination: PageMetadata;
}

function invalidInput(label: string): never {
  throw new ApiError('invalid_request', `The ${label} is invalid.`, 400);
}

function basePath(role: CharityRole): string {
  if (role === 'admin') return '/admin/api';
  if (role === 'steward') return '/api/steward';
  return invalidInput('management role');
}

function pageArguments(page: unknown, pageSize: unknown): asserts page is string {
  if (!isPageNumber(page)) invalidInput('page');
  if (!isPageSize(pageSize)) invalidInput('page size');
}

function queryText(value: unknown, label: string): string {
  if (typeof value !== 'string' || !validManagementSearch(value, 512)) {
    invalidResponse(label);
  }
  return value;
}

function inputRecord(
  value: unknown,
  allowed: readonly string[],
  label: string,
): Record<string, unknown> {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    return invalidInput(label);
  }
  const result = value as Record<string, unknown>;
  if (Object.keys(result).some((key) => !allowed.includes(key))) invalidInput(label);
  return result;
}

function inputID(value: unknown, label: string): string {
  if (typeof value !== 'string' || !managementResourceID(value)) invalidInput(label);
  return value;
}

function inputOneOf<const T extends string>(
  value: unknown,
  values: readonly T[],
  label: string,
): T {
  if (typeof value !== 'string' || !values.includes(value as T)) invalidInput(label);
  return value as T;
}

function inputCandidateFilters(value: unknown): Required<CharityBindingCandidatePageFilters> {
  const root = inputRecord(
    value,
    ['donation_id', 'donation_key_id', 'source', 'q'],
    'binding candidate page filters',
  );
  const donationID =
    root.donation_id === undefined ? '' : inputID(root.donation_id, 'donation id filter');
  const donationKeyID =
    root.donation_key_id === undefined
      ? ''
      : inputID(root.donation_key_id, 'donation key id filter');
  const source =
    root.source === undefined
      ? ''
      : inputOneOf(root.source, ['', 'automatic', 'manual'] as const, 'candidate source filter');
  const q =
    root.q === undefined
      ? ''
      : typeof root.q === 'string' && validManagementSearch(root.q, 512)
        ? root.q
        : invalidInput('candidate search');
  return { donation_id: donationID, donation_key_id: donationKeyID, source, q };
}

function candidateFilters(
  value: CharityBindingCandidatePageFilters,
): Required<CharityBindingCandidatePageFilters> {
  const root = record(
    value,
    ['donation_id', 'donation_key_id', 'source', 'q'],
    'binding candidate page filters',
    [],
  );
  const donationID =
    root.donation_id === undefined ? '' : decimalID(root.donation_id, 'donation id filter');
  const donationKeyID =
    root.donation_key_id === undefined
      ? ''
      : decimalID(root.donation_key_id, 'donation key id filter');
  const source =
    root.source === undefined
      ? ''
      : oneOf(root.source, ['', 'automatic', 'manual'] as const, 'candidate source filter');
  const q = root.q === undefined ? '' : queryText(root.q, 'candidate search');
  return { donation_id: donationID, donation_key_id: donationKeyID, source, q };
}

function normalizeCandidatePage(
  value: unknown,
  role: CharityRole,
  filters: CharityBindingCandidatePageFilters,
  page: string,
  pageSize: PageSize,
): CharityBindingCandidatePage {
  basePath(role);
  pageArguments(page, pageSize);
  const normalizedFilters = candidateFilters(filters);
  const root = record(
    value,
    ['data', 'next_cursor', 'pagination'],
    `${role} binding candidate page`,
  );
  if (root.next_cursor !== null) invalidResponse(`${role} binding candidate cursor`);
  const metadata = normalizePageMetadata(root.pagination);
  const data = array(root.data, `${role} binding candidate data`, 100).map((entry, index) =>
    normalizeCharityBindingCandidate(entry, `${role} binding candidate ${index + 1}`),
  );
  validatePageResponse(metadata, page, pageSize, data.length);
  if (
    normalizedFilters.donation_id !== '' &&
    data.some((entry) => entry.donation_id !== normalizedFilters.donation_id)
  ) {
    invalidResponse(`${role} binding candidate donation filter`);
  }
  if (
    normalizedFilters.donation_key_id !== '' &&
    data.some((entry) => entry.donation_key_id !== normalizedFilters.donation_key_id)
  ) {
    invalidResponse(`${role} binding candidate key filter`);
  }
  const sourceFilter = normalizedFilters.source;
  if (
    (sourceFilter === 'automatic' || sourceFilter === 'manual') &&
    data.some((entry) => !entry.source_types.includes(sourceFilter))
  ) {
    invalidResponse(`${role} binding candidate source filter`);
  }
  const identities = data.map((entry) => `${entry.donation_key_id}:${entry.upstream_model_id}`);
  if (new Set(identities).size !== identities.length) {
    invalidResponse(`${role} binding candidate identities`);
  }
  return { data, next_cursor: null, pagination: metadata };
}

export function normalizeCharityBindingCandidatesPage(
  value: unknown,
  role: CharityRole,
  filters: CharityBindingCandidatePageFilters,
  page: string,
  pageSize: PageSize,
): CharityBindingCandidatePage {
  return normalizeCandidatePage(value, role, filters, page, pageSize);
}

export function getManagedBindingCandidatesPage(
  role: CharityRole,
  modelId: string,
  filters: CharityBindingCandidatePageFilters = {},
  page: string,
  pageSize: PageSize,
  signal?: AbortSignal,
): Promise<CharityBindingCandidatePage> {
  const root = basePath(role);
  if (typeof modelId !== 'string' || !managementResourceID(modelId)) {
    invalidInput(`${role} charity model id`);
  }
  const id = modelId;
  const normalizedFilters = inputCandidateFilters(filters);
  pageArguments(page, pageSize);
  return decoded(
    queryPath(`${root}/charity-models/${encodeURIComponent(id)}/binding-candidates`, {
      donation_id: normalizedFilters.donation_id || undefined,
      donation_key_id: normalizedFilters.donation_key_id || undefined,
      source: normalizedFilters.source || undefined,
      q: normalizedFilters.q || undefined,
      page,
      page_size: pageSize,
    }),
    (value) => normalizeCandidatePage(value, role, filters, page, pageSize),
    { signal },
  );
}
