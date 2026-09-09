import { ApiError } from '@shared/query/http';
import { decoded, queryPath } from './api';
import {
  normalizeAdminCharityModel,
  normalizeStewardCharityModel,
  type CharityModel,
  type CharityRole,
} from './charity';
import { array, invalidResponse, record } from './wire';
import {
  isPageNumber,
  isPageSize,
  normalizePageMetadata,
  validatePageResponse,
  type PageSize,
} from './pageNumbers';

export function validManagementSearch(value: string, maximum = 128): boolean {
  return (
    Array.from(value).length <= maximum &&
    new TextEncoder().encode(value).byteLength <= maximum * 4 &&
    Array.from(value).every((character) => {
      const point = character.codePointAt(0) ?? 0;
      return (
        point >= 0x20 && !(point >= 0x7f && point <= 0x9f) && !(point >= 0xd800 && point <= 0xdfff)
      );
    })
  );
}

export function managementResourceID(value: string): boolean {
  return /^[1-9][0-9]{0,18}$/.test(value) && BigInt(value) <= 9_223_372_036_854_775_807n;
}

function base(role: CharityRole): string {
  if (role !== 'admin' && role !== 'steward')
    throw new ApiError('invalid_request', 'Invalid management role.', 400);
  return role === 'admin' ? '/admin/api/charity-models' : '/api/steward/charity-models';
}

export function getManagedCharityModelsPage(
  role: CharityRole,
  query: string,
  enabled: string,
  page: string,
  pageSize: PageSize,
  signal?: AbortSignal,
) {
  const path = base(role);
  const asciiLower = (text: string) => text.replace(/[A-Z]/g, (letter) => letter.toLowerCase());
  if (
    !validManagementSearch(query) ||
    !['', 'true', 'false'].includes(enabled) ||
    !isPageNumber(page) ||
    !isPageSize(pageSize)
  ) {
    throw new ApiError('invalid_request', 'Invalid model page.', 400);
  }
  return decoded(
    queryPath(path, { q: query, enabled, page, page_size: pageSize }),
    (value) => {
      const root = record(value, ['data', 'pagination', 'next_cursor'], 'charity models page');
      if (root.next_cursor !== null) invalidResponse('charity models page cursor');
      const data = array(root.data, 'charity models', pageSize).map(
        role === 'admin' ? normalizeAdminCharityModel : normalizeStewardCharityModel,
      );
      const pagination = normalizePageMetadata(root.pagination);
      validatePageResponse(pagination, page, pageSize, data.length);
      if (
        new Set(data.map((row) => row.id)).size !== data.length ||
        data.some(
          (row) =>
            (enabled !== '' && row.enabled !== (enabled === 'true')) ||
            (query !== '' &&
              ![row.provider, row.model, row.full_name].some((name) =>
                asciiLower(name).includes(asciiLower(query)),
              )),
        )
      )
        invalidResponse('charity model page rows');
      return { data, pagination, next_cursor: null };
    },
    { signal },
  );
}

export function getManagedCharityModel(
  role: CharityRole,
  id: string,
  signal?: AbortSignal,
): Promise<CharityModel> {
  const path = base(role);
  if (!managementResourceID(id)) throw new ApiError('invalid_request', 'Invalid model ID.', 400);
  return decoded(
    `${path}/${id}`,
    (value) => {
      const model = (role === 'admin' ? normalizeAdminCharityModel : normalizeStewardCharityModel)(
        value,
      );
      if (model.id !== id) invalidResponse('charity model identity');
      return model;
    },
    { signal },
  );
}
