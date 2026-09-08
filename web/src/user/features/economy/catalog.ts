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
} from '@shared/operations/pageNumbers';
import { normalizeCharityCapabilityModel } from './normalize';
import type { CharityCapabilityModel, DonationIntakeState } from './types';

export type CatalogAvailability =
  'feature_disabled' | 'model_disabled' | 'level_denied' | 'no_usable_key' | 'available';

export type CatalogAccessFilter = 'all' | 'true' | 'false';

export interface CatalogFilter {
  page: string;
  pageSize: PageSize;
  query: string;
  allowedForMe: CatalogAccessFilter;
}

export interface CatalogModel extends CharityCapabilityModel {
  publicDescription: string;
  enabled: boolean;
  allowedLevels: number[];
  levelAllowed: boolean;
  availability: CatalogAvailability;
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
      filter.page,
      filter.pageSize,
    ] as const,
};

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

function normalizedFilter(filter: CatalogFilter): CatalogFilter {
  return {
    page: catalogPage(filter.page),
    pageSize: catalogPageSize(filter.pageSize),
    query: catalogQuery(filter.query),
    allowedForMe: catalogAccess(filter.allowedForMe),
  };
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
  if (
    (availability === 'model_disabled' && enabled) ||
    (availability === 'level_denied' && (!enabled || levelAllowed)) ||
    ((availability === 'no_usable_key' || availability === 'available') &&
      (!enabled || !levelAllowed))
  ) {
    invalidResponse('charity catalog availability state');
  }
  return {
    ...capability,
    publicDescription: normalizePublicDescription(root.public_description),
    enabled,
    allowedLevels,
    levelAllowed,
    availability,
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
  return normalizeCharityCatalog(
    await apiFetch<unknown>(`/api/charity/models?${query.toString()}`, { signal }),
  );
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
