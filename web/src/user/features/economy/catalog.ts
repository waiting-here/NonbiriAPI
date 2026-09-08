import { useEffect, useRef } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { clearStationSession } from '@shared/charityManagement';
import { isForbidden, isUnauthorized, apiFetch, ApiError } from '@shared/query/http';
import {
  array,
  boolean,
  integer,
  invalidResponse,
  oneOf,
  record,
  string,
  unixSecond,
} from '@shared/operations/wire';
import {
  isPageNumber,
  normalizePageMetadata,
  PAGE_SIZES,
  type PageMetadata,
  type PageSize,
  validatePageResponse,
} from '@shared/operations/pageNumbers';
import { normalizeCharityCapabilityModel } from './normalize';
import type { CharityCapabilityModel, DonationIntakeState } from './types';

export type CatalogAvailability =
  'feature_disabled' | 'model_disabled' | 'level_denied' | 'no_usable_key' | 'available';

export type CatalogAccessFilter = 'all' | 'true' | 'false';
export type CatalogLevelFilter = 'all' | '1' | '2' | '3' | '4' | '5';
export type CatalogAvailabilityFilter = CatalogAccessFilter;

export interface CharityCatalogUrlFilters {
  query: string;
  allowedForMe: CatalogAccessFilter;
  allowedLevel: CatalogLevelFilter;
  currentlyAvailable: CatalogAvailabilityFilter;
}

export interface CatalogFilter {
  page: string;
  pageSize: PageSize;
  query: string;
  allowedForMe: CatalogAccessFilter;
  allowedLevel: CatalogLevelFilter;
  currentlyAvailable: CatalogAvailabilityFilter;
}

export interface CatalogModel extends CharityCapabilityModel {
  publicDescription: string;
  enabled: boolean;
  allowedLevels: number[];
  levelAllowed: boolean;
  availability: CatalogAvailability;
  currentlyAvailable: boolean;
}

export interface CharityCatalog {
  models: CatalogModel[];
  pagination: PageMetadata;
  donationIntake: DonationIntakeState;
  serverNow: number;
}

export const charityCatalogKeys = {
  all: ['user', 'economy', 'charity-catalog'] as const,
  page: (accountID: string, filter: CatalogFilter) =>
    [
      'user',
      'economy',
      'charity-catalog',
      accountID,
      filter.query,
      filter.allowedForMe,
      filter.allowedLevel,
      filter.currentlyAvailable,
      filter.page,
      filter.pageSize,
    ] as const,
};

export const DEFAULT_CHARITY_CATALOG_FILTERS: CharityCatalogUrlFilters = {
  query: '',
  allowedForMe: 'true',
  allowedLevel: 'all',
  currentlyAvailable: 'true',
};

const CATALOG_URL_FILTER_NAMES = {
  query: 'q',
  allowedForMe: 'allowed_for_me',
  allowedLevel: 'allowed_level',
  currentlyAvailable: 'currently_available',
} as const;

const CATALOG_AVAILABILITIES: readonly CatalogAvailability[] = [
  'feature_disabled',
  'model_disabled',
  'level_denied',
  'no_usable_key',
  'available',
];
const MAX_QUERY_CODE_POINTS = 128;
const MAX_QUERY_BYTES = 512;

function invalidRequest(field: string): never {
  throw new ApiError('invalid_request', `Invalid ${field}.`, 400);
}

function wellFormedUTF8(value: string): boolean {
  for (const character of value) {
    const codePoint = character.codePointAt(0) ?? 0;
    if (codePoint >= 0xd800 && codePoint <= 0xdfff) return false;
  }
  return true;
}

function catalogQuery(value: string): string {
  if (
    typeof value !== 'string' ||
    !wellFormedUTF8(value) ||
    Array.from(value).length > MAX_QUERY_CODE_POINTS ||
    new TextEncoder().encode(value).byteLength > MAX_QUERY_BYTES ||
    value.includes('\u0000')
  ) {
    invalidRequest('catalog search');
  }
  return value;
}

function catalogPage(value: string): string {
  if (!isPageNumber(value)) invalidRequest('catalog page');
  return value;
}

function catalogPageSize(value: PageSize): PageSize {
  if (!PAGE_SIZES.includes(value)) invalidRequest('catalog page size');
  return value;
}

function catalogAccess(value: CatalogAccessFilter): CatalogAccessFilter {
  if (value !== 'all' && value !== 'true' && value !== 'false') {
    invalidRequest('catalog access filter');
  }
  return value;
}

function catalogLevel(value: CatalogLevelFilter): CatalogLevelFilter {
  if (value !== 'all' && !/^[1-5]$/.test(value)) {
    invalidRequest('catalog allowed level filter');
  }
  return value;
}

function normalizedFilter(filter: CatalogFilter): CatalogFilter {
  return {
    page: catalogPage(filter.page),
    pageSize: catalogPageSize(filter.pageSize),
    query: catalogQuery(filter.query),
    allowedForMe: catalogAccess(filter.allowedForMe),
    allowedLevel: catalogLevel(filter.allowedLevel),
    currentlyAvailable: catalogAccess(filter.currentlyAvailable),
  };
}

function validCatalogQuery(value: string): boolean {
  try {
    catalogQuery(value);
    return true;
  } catch {
    return false;
  }
}

function readSingleSearchParam(
  params: URLSearchParams,
  name: string,
): { value: string | undefined; needsNormalization: boolean } {
  const values = params.getAll(name);
  return {
    value: values.length === 1 ? values[0] : undefined,
    needsNormalization: values.length > 1,
  };
}

function readCatalogAccessParam(
  params: URLSearchParams,
  name: string,
  defaultValue: CatalogAccessFilter,
): { value: CatalogAccessFilter; needsNormalization: boolean } {
  const single = readSingleSearchParam(params, name);
  if (single.value === undefined) {
    return {
      value: defaultValue,
      needsNormalization: single.needsNormalization,
    };
  }
  if (single.value === 'all' || single.value === 'true' || single.value === 'false') {
    return { value: single.value, needsNormalization: single.needsNormalization };
  }
  return { value: defaultValue, needsNormalization: true };
}

function readCatalogLevelParam(params: URLSearchParams): {
  value: CatalogLevelFilter;
  needsNormalization: boolean;
} {
  const single = readSingleSearchParam(params, CATALOG_URL_FILTER_NAMES.allowedLevel);
  if (single.value === undefined) {
    return {
      value: 'all',
      needsNormalization: single.needsNormalization,
    };
  }
  if (single.value === 'all' || /^[1-5]$/.test(single.value)) {
    return {
      value: single.value as CatalogLevelFilter,
      needsNormalization: single.needsNormalization,
    };
  }
  return { value: 'all', needsNormalization: true };
}

export interface CharityCatalogUrlState {
  filters: CharityCatalogUrlFilters;
  needsNormalization: boolean;
}

/** Parse committed catalog filters without allowing malformed URL values to reach the API. */
export function readCharityCatalogUrlState(params: URLSearchParams): CharityCatalogUrlState {
  const queryParam = readSingleSearchParam(params, CATALOG_URL_FILTER_NAMES.query);
  const query =
    queryParam.value !== undefined && queryParam.value !== '' && validCatalogQuery(queryParam.value)
      ? queryParam.value
      : '';
  const queryNeedsNormalization =
    queryParam.needsNormalization ||
    (queryParam.value !== undefined && (queryParam.value === '' || query !== queryParam.value));
  const allowedForMe = readCatalogAccessParam(
    params,
    CATALOG_URL_FILTER_NAMES.allowedForMe,
    DEFAULT_CHARITY_CATALOG_FILTERS.allowedForMe,
  );
  const allowedLevel = readCatalogLevelParam(params);
  const currentlyAvailable = readCatalogAccessParam(
    params,
    CATALOG_URL_FILTER_NAMES.currentlyAvailable,
    DEFAULT_CHARITY_CATALOG_FILTERS.currentlyAvailable,
  );
  return {
    filters: {
      query,
      allowedForMe: allowedForMe.value,
      allowedLevel: allowedLevel.value,
      currentlyAvailable: currentlyAvailable.value,
    },
    needsNormalization:
      queryNeedsNormalization ||
      allowedForMe.needsNormalization ||
      allowedLevel.needsNormalization ||
      currentlyAvailable.needsNormalization,
  };
}

/** Return a canonical URL while preserving route-owned parameters such as tab. */
export function canonicalCharityCatalogSearch(params: URLSearchParams): URLSearchParams {
  const state = readCharityCatalogUrlState(params);
  const next = new URLSearchParams(params);
  if (state.filters.query === '') next.delete(CATALOG_URL_FILTER_NAMES.query);
  else {
    next.delete(CATALOG_URL_FILTER_NAMES.query);
    next.set(CATALOG_URL_FILTER_NAMES.query, state.filters.query);
  }
  if (state.needsNormalization) {
    next.delete(CATALOG_URL_FILTER_NAMES.allowedForMe);
    next.set(CATALOG_URL_FILTER_NAMES.allowedForMe, state.filters.allowedForMe);
    next.delete(CATALOG_URL_FILTER_NAMES.allowedLevel);
    next.set(CATALOG_URL_FILTER_NAMES.allowedLevel, state.filters.allowedLevel);
    next.delete(CATALOG_URL_FILTER_NAMES.currentlyAvailable);
    next.set(CATALOG_URL_FILTER_NAMES.currentlyAvailable, state.filters.currentlyAvailable);
  }
  return next;
}

/** Write an explicit filter selection; `all` remains visible in the URL. */
export function writeCharityCatalogFilters(
  params: URLSearchParams,
  filters: CharityCatalogUrlFilters,
): URLSearchParams {
  const next = new URLSearchParams(params);
  const normalized: CharityCatalogUrlFilters = {
    query: catalogQuery(filters.query),
    allowedForMe: catalogAccess(filters.allowedForMe),
    allowedLevel: catalogLevel(filters.allowedLevel),
    currentlyAvailable: catalogAccess(filters.currentlyAvailable),
  };
  const values: [string, string][] = [
    [CATALOG_URL_FILTER_NAMES.query, normalized.query],
    [CATALOG_URL_FILTER_NAMES.allowedForMe, normalized.allowedForMe],
    [CATALOG_URL_FILTER_NAMES.allowedLevel, normalized.allowedLevel],
    [CATALOG_URL_FILTER_NAMES.currentlyAvailable, normalized.currentlyAvailable],
  ];
  for (const [name, value] of values) {
    next.delete(name);
    if (value !== '') next.set(name, value);
  }
  return next;
}

export function charityCatalogFilterKey(filters: CharityCatalogUrlFilters): string {
  return [
    filters.query,
    filters.allowedForMe,
    filters.allowedLevel,
    filters.currentlyAvailable,
  ].join('\u001f');
}

function normalizePublicDescription(value: unknown): string {
  const description = string(value, 'charity catalog public description', {
    max: 1_024,
    bytes: 4_096,
    multiline: true,
  });
  if (!wellFormedUTF8(description) || description.includes('\r')) {
    invalidResponse('charity catalog public description');
  }
  for (const character of description) {
    const codePoint = character.codePointAt(0) ?? 0;
    if (codePoint >= 0x7f && codePoint <= 0x9f) {
      invalidResponse('charity catalog public description');
    }
  }
  return description;
}

function normalizeAllowedLevels(value: unknown): number[] {
  const raw = array(value, 'charity catalog allowed levels', 5);
  const levels = raw.map((entry) => integer(entry, 'charity catalog allowed level', 1, 5));
  for (let index = 1; index < levels.length; index += 1) {
    if (levels[index] <= levels[index - 1]) invalidResponse('charity catalog allowed levels');
  }
  return levels;
}

export function normalizeCatalogModel(value: unknown): CatalogModel {
  const root = record(
    value,
    [
      'id',
      'provider',
      'model',
      'full_name',
      'pricing',
      'discount',
      'public_description',
      'enabled',
      'allowed_levels',
      'level_allowed',
      'currently_available',
      'availability',
    ],
    'charity catalog model',
  );
  const capability = normalizeCharityCapabilityModel({
    id: root.id,
    provider: root.provider,
    model: root.model,
    full_name: root.full_name,
    pricing: root.pricing,
    discount: root.discount,
  });
  const enabled = boolean(root.enabled, 'charity catalog enabled');
  const allowedLevels = normalizeAllowedLevels(root.allowed_levels);
  const levelAllowed = boolean(root.level_allowed, 'charity catalog level allowance');
  const availability = oneOf(
    root.availability,
    CATALOG_AVAILABILITIES,
    'charity catalog availability',
  );
  const currentlyAvailable = boolean(
    root.currently_available,
    'charity catalog current availability',
  );
  if (
    (availability === 'model_disabled' && enabled) ||
    (availability === 'level_denied' && (!enabled || levelAllowed)) ||
    ((availability === 'no_usable_key' || availability === 'available') &&
      (!enabled || !levelAllowed))
  ) {
    invalidResponse('charity catalog availability state');
  }
  if (
    ((availability === 'feature_disabled' ||
      availability === 'model_disabled' ||
      availability === 'no_usable_key') &&
      currentlyAvailable) ||
    (availability === 'available' && !currentlyAvailable)
  ) {
    invalidResponse('charity catalog current availability state');
  }
  return {
    ...capability,
    publicDescription: normalizePublicDescription(root.public_description),
    enabled,
    allowedLevels,
    levelAllowed,
    availability,
    currentlyAvailable,
  };
}

export function normalizeCharityCatalog(value: unknown): CharityCatalog {
  const root = record(
    value,
    ['models', 'pagination', 'donation_intake', 'server_now'],
    'charity catalog',
  );
  const pagination = normalizePageMetadata(root.pagination);
  const models = array(root.models, 'charity catalog models', pagination.page_size).map(
    normalizeCatalogModel,
  );
  const total = BigInt(pagination.total_items);
  const page = BigInt(pagination.page);
  const pageSize = BigInt(pagination.page_size);
  const remaining = total - (page - 1n) * pageSize;
  const expectedItems = remaining > pageSize ? pageSize : remaining;
  if (expectedItems < 0n || BigInt(models.length) !== expectedItems) {
    invalidResponse('charity catalog page');
  }
  const ids = new Set<string>();
  const names = new Set<string>();
  for (const model of models) {
    if (ids.has(model.id)) invalidResponse('charity catalog model identities');
    if (names.has(model.fullName)) invalidResponse('charity catalog model names');
    ids.add(model.id);
    names.add(model.fullName);
  }
  return {
    models,
    pagination,
    donationIntake: oneOf(
      root.donation_intake,
      ['open', 'closed'] as const,
      'charity catalog donation intake',
    ),
    serverNow: unixSecond(root.server_now, 'charity catalog server time'),
  };
}

export async function getCharityCatalog(
  filter: CatalogFilter,
  signal?: AbortSignal,
): Promise<CharityCatalog> {
  const normalized = normalizedFilter(filter);
  const query = new URLSearchParams({
    view: 'catalog',
    page: normalized.page,
    page_size: String(normalized.pageSize),
  });
  if (normalized.query !== '') query.set('q', normalized.query);
  if (normalized.allowedForMe !== 'all') query.set('allowed_for_me', normalized.allowedForMe);
  if (normalized.allowedLevel !== 'all') query.set('allowed_level', normalized.allowedLevel);
  if (normalized.currentlyAvailable !== 'all') {
    query.set('currently_available', normalized.currentlyAvailable);
  }
  const result = normalizeCharityCatalog(
    await apiFetch<unknown>(`/api/charity/models?${query.toString()}`, { signal }),
  );
  validatePageResponse(
    result.pagination,
    normalized.page,
    normalized.pageSize,
    result.models.length,
  );
  return result;
}

export function useCharityCatalog(
  accountID: string | undefined,
  filter: CatalogFilter,
  enabled = true,
) {
  const queryClient = useQueryClient();
  const handledError = useRef<unknown>(null);
  const queryKey = accountID
    ? charityCatalogKeys.page(accountID, filter)
    : ([...charityCatalogKeys.all, 'none'] as const);
  const query = useQuery({
    queryKey,
    queryFn: ({ signal }) => getCharityCatalog(filter, signal),
    enabled: enabled && Boolean(accountID),
    placeholderData: (previous, previousQuery) =>
      previousQuery?.queryKey[3] === accountID ? previous : undefined,
    retry: false,
    staleTime: 15_000,
  });
  useEffect(() => {
    if (accountID === undefined) {
      handledError.current = null;
      return;
    }
    if (
      !query.error ||
      handledError.current === query.error ||
      (!isUnauthorized(query.error) && !isForbidden(query.error))
    ) {
      return;
    }
    handledError.current = query.error;
    clearStationSession(queryClient, 'steward');
  }, [accountID, query.error, queryClient]);
  return query;
}
