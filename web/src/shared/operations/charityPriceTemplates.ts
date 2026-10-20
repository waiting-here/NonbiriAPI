import { decoded, idempotentOptions, queryPath } from './api';
import { normalizeCharityPriceConfig, type CharityPriceConfig, type CharityRole } from './charity';
import {
  array,
  boolean,
  decimal,
  decimalID,
  invalidResponse,
  record,
  string,
  unixSecond,
} from './wire';
import { normalizePageMetadata, validatePageResponse, type PageSize } from './pageNumbers';

export interface CharityPriceTemplate extends CharityPriceConfig {
  id: string;
  name: string;
  description: string;
  enabled: boolean;
  revision: string;
  created_at: number;
  updated_at: number;
}

export type PriceTemplateInput = CharityPriceConfig &
  Pick<CharityPriceTemplate, 'name' | 'description' | 'enabled'>;

function base(role: CharityRole) {
  return `${role === 'admin' ? '/admin/api' : '/api/steward'}/charity-price-templates`;
}

export function normalizePriceTemplate(value: unknown): CharityPriceTemplate {
  const row = record(
    value,
    [
      'id',
      'name',
      'description',
      'enabled',
      'revision',
      'pricing',
      'token_reserve_credits',
      'discount',
      'recurring_discount',
      'created_at',
      'updated_at',
    ],
    'price template',
  );
  return {
    ...normalizeCharityPriceConfig(row, 'price template'),
    id: decimalID(row.id, 'price template id'),
    name: string(row.name, 'price template name', { min: 1 }),
    description: string(row.description, 'price template description', { multiline: true }),
    enabled: boolean(row.enabled, 'price template enabled'),
    revision: decimal(row.revision, 'price template revision', { positive: true }),
    created_at: unixSecond(row.created_at, 'price template creation'),
    updated_at: unixSecond(row.updated_at, 'price template update'),
  };
}

export function getPriceTemplates(
  role: CharityRole,
  q: string,
  state: 'enabled' | 'disabled' | 'all',
  page: string,
  pageSize: PageSize,
  signal?: AbortSignal,
) {
  return decoded(
    queryPath(base(role), { q, state, page, page_size: pageSize }),
    (value) => {
      const root = record(value, ['data', 'pagination', 'next_cursor'], 'price templates page');
      if (root.next_cursor !== null) invalidResponse('price templates cursor');
      const pagination = normalizePageMetadata(root.pagination);
      validatePageResponse(pagination, page, pageSize);
      return {
        data: array(root.data, 'price templates', pageSize).map(normalizePriceTemplate),
        pagination,
      };
    },
    { signal },
  );
}

export function getPriceTemplate(role: CharityRole, id: string, signal?: AbortSignal) {
  return decoded(`${base(role)}/${decimalID(id, 'template id')}`, normalizePriceTemplate, {
    signal,
  });
}

export function savePriceTemplate(
  role: CharityRole,
  input: PriceTemplateInput & { id?: string; expected_revision?: string },
  key: string,
) {
  const { id, ...body } = input;
  return decoded(
    id ? `${base(role)}/${decimalID(id, 'template id')}` : base(role),
    normalizePriceTemplate,
    idempotentOptions(key, { method: id ? 'PUT' : 'POST', json: body }),
  );
}

export function deletePriceTemplate(role: CharityRole, id: string, revision: string, key: string) {
  return decoded(
    `${base(role)}/${decimalID(id, 'template id')}`,
    () => undefined,
    idempotentOptions(key, { method: 'DELETE', json: { expected_revision: revision } }),
  );
}
