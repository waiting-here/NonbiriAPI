import { apiFetch } from '@shared/query/http';
import { queryPath } from '@shared/operations/api';
import { array, decimal, integer, record, string } from '@shared/operations/wire';
import type { PageSize } from '@shared/operations/pageNumbers';

export interface Subject {
  id: string;
  name: string;
  role: string;
}
export interface Filter {
  time_from?: number;
  time_to?: number;
  source?: string;
  actor_id?: string;
  authorizer_id?: string;
  domain?: string;
  action?: string;
  outcome?: string;
  event_id?: string;
  target_kind?: string;
  target_id?: string;
  group_key?: string;
  search?: string;
}
interface Counts {
  targets: string;
  succeeded: string;
  skipped: string;
  failed: string;
}
export interface Operation extends Counts {
  target_name?: string;
  id: string;
  event_id: string;
  created_at: number;
  source: string;
  executor_kind: string;
  actor: Subject;
  authorizer: Subject;
  authorization_ref?: string;
  domain: string;
  action: string;
  correlation_kind?: string;
  correlation_id?: string;
  outcome: string;
  code: string;
  summary: string;
}
export interface Group extends Counts {
  key: string;
  bucket: number;
  source: string;
  domain: string;
  action: string;
  actor: Subject;
  authorizer: Subject;
  outcome: string;
  operations: string;
}
export interface Item {
  ordinal: string;
  action: string;
  target_kind: string;
  target_id: string;
  target_name: string;
  outcome: string;
  code: string;
  changes: Change[];
  details?: SnapshotDetails;
}
export interface Change {
  field: string;
  operation: string;
  before?: string;
  after?: string;
  unit?: string;
  truncated?: boolean;
}
export interface SnapshotDetails {
  version?: number;
  previous_name?: string;
  state?: string;
  truncated?: boolean;
  related?: {
    kind: string;
    id: string;
    name: string;
    previous_name?: string;
    state: string;
    changes?: Change[];
  }[];
  impacts?: {
    kind: string;
    created: string;
    updated: string;
    deleted: string;
    sampled: string;
    omitted: string;
  }[];
}
interface Pagination {
  total: string;
  page: number;
  page_size: PageSize;
}
export interface HistoryPage extends Pagination {
  view: string;
  operations: Operation[];
  groups: Group[];
  snapshot_max_id: string;
}
export interface ItemPage extends Pagination {
  items: Item[];
}
export interface PreparedCleanup {
  filter: Filter;
  all: boolean;
  cutoff_max_id: string;
}
export interface CleanupJob {
  id: string;
  state: string;
  created_at: number;
  updated_at: number;
  actor: Subject;
  filter: Filter;
  cutoff_max_id: string;
  deleted_operations: string;
  deleted_items: string;
  skipped_active: string;
  error_code: string;
}
const base = '/admin/api/management-operations';
export const historyRoot = (account: string) => ['admin', 'management-history', account] as const;

function subject(value: unknown): Subject {
  const row = record(value, ['id', 'name', 'role'], 'operator');
  return {
    id: string(row.id, 'operator ID'),
    name: string(row.name, 'operator name'),
    role: string(row.role, 'operator role'),
  };
}
function counts(value: unknown): Counts {
  const row = record(value, ['targets', 'succeeded', 'skipped', 'failed'], 'operation totals');
  return {
    targets: decimal(row.targets, 'targets'),
    succeeded: decimal(row.succeeded, 'succeeded'),
    skipped: decimal(row.skipped, 'skipped'),
    failed: decimal(row.failed, 'failed'),
  };
}
function operation(value: unknown): Operation {
  const row = record(
    value,
    [
      'id',
      'event_id',
      'created_at',
      'source',
      'executor_kind',
      'actor',
      'authorizer',
      'domain',
      'action',
      'outcome',
      'code',
      'summary',
      'authorization_ref',
      'correlation_kind',
      'correlation_id',
      'target_name',
    ],
    'management operation',
    [
      'id',
      'event_id',
      'created_at',
      'source',
      'executor_kind',
      'actor',
      'authorizer',
      'domain',
      'action',
      'outcome',
      'code',
      'summary',
    ],
  );
  return {
    ...counts(value),
    ...(typeof row.target_name === 'string' ? { target_name: row.target_name } : {}),
    id: decimal(row.id, 'operation ID'),
    event_id: string(row.event_id, 'event ID'),
    created_at: integer(row.created_at, 'operation time'),
    source: string(row.source, 'source'),
    executor_kind: string(row.executor_kind, 'executor'),
    actor: subject(row.actor),
    authorizer: subject(row.authorizer),
    domain: string(row.domain, 'domain'),
    action: string(row.action, 'action'),
    outcome: string(row.outcome, 'outcome'),
    code: string(row.code, 'code'),
    summary: string(row.summary, 'summary'),
    ...(typeof row.authorization_ref === 'string'
      ? { authorization_ref: row.authorization_ref }
      : {}),
    ...(typeof row.correlation_kind === 'string' ? { correlation_kind: row.correlation_kind } : {}),
    ...(typeof row.correlation_id === 'string' ? { correlation_id: row.correlation_id } : {}),
  };
}
function group(value: unknown): Group {
  const row = record(
    value,
    ['key', 'bucket', 'source', 'domain', 'action', 'actor', 'authorizer', 'outcome', 'operations'],
    'operation group',
  );
  return {
    ...counts(value),
    key: string(row.key, 'group'),
    bucket: integer(row.bucket, 'group time'),
    source: string(row.source, 'source'),
    domain: string(row.domain, 'domain'),
    action: string(row.action, 'action'),
    actor: subject(row.actor),
    authorizer: subject(row.authorizer),
    outcome: string(row.outcome, 'outcome'),
    operations: decimal(row.operations, 'operations'),
  };
}
function pagination(value: unknown): Pagination {
  const row = record(value, ['total', 'page', 'page_size'], 'pagination');
  return {
    total: decimal(row.total, 'total'),
    page: integer(row.page, 'page', 1),
    page_size: integer(row.page_size, 'page size', 1, 100) as PageSize,
  };
}
function historyPage(value: unknown): HistoryPage {
  const row = record(
    value,
    ['view', 'operations', 'groups', 'snapshot_max_id'],
    'management history',
  );
  return {
    ...pagination(value),
    view: string(row.view, 'view'),
    operations: array(row.operations, 'operations', 100).map(operation),
    groups: array(row.groups, 'groups', 100).map(group),
    snapshot_max_id: decimal(row.snapshot_max_id, 'history snapshot'),
  };
}
function itemPage(value: unknown): ItemPage {
  const row = record(value, ['items'], 'operation targets');
  return {
    ...pagination(value),
    items: array(row.items, 'targets', 100).map((value) => {
      const item = record(
        value,
        [
          'ordinal',
          'action',
          'target_kind',
          'target_id',
          'target_name',
          'outcome',
          'code',
          'changes',
          'details',
        ],
        'target',
        ['ordinal', 'action', 'target_kind', 'target_id', 'target_name', 'outcome', 'code'],
      );
      return {
        ordinal: decimal(item.ordinal, 'target ordinal'),
        action: string(item.action, 'action'),
        target_kind: string(item.target_kind, 'target kind'),
        target_id: string(item.target_id, 'target ID'),
        target_name: string(item.target_name, 'target name'),
        outcome: string(item.outcome, 'outcome'),
        code: string(item.code, 'code'),
        ...(item.details && typeof item.details === 'object'
          ? { details: snapshotDetails(item.details) }
          : {}),
        changes: array(item.changes ?? [], 'changes').map((change) => {
          const entry = record(
            change,
            ['field', 'operation', 'before', 'after', 'unit', 'truncated'],
            'field change',
            ['field', 'operation'],
          );
          return {
            field: string(entry.field, 'field'),
            operation: string(entry.operation, 'operation'),
            ...(typeof entry.before === 'string' ? { before: entry.before } : {}),
            ...(typeof entry.after === 'string' ? { after: entry.after } : {}),
            ...(typeof entry.unit === 'string' ? { unit: entry.unit } : {}),
            ...(entry.truncated === true ? { truncated: true } : {}),
          };
        }),
      };
    }),
  };
}
function snapshotDetails(value: unknown): SnapshotDetails {
  const row = record(
    value,
    ['version', 'previous_name', 'state', 'truncated', 'related', 'impacts'],
    'snapshot',
    [],
  );
  return {
    ...(typeof row.version === 'number' ? { version: row.version } : {}),
    ...(typeof row.previous_name === 'string' ? { previous_name: row.previous_name } : {}),
    ...(typeof row.state === 'string' ? { state: row.state } : {}),
    ...(row.truncated === true ? { truncated: true } : {}),
    ...(Array.isArray(row.related)
      ? {
          related: row.related.map((value) => {
            const object = record(
              value,
              ['kind', 'id', 'name', 'state', 'previous_name', 'changes'],
              'related object',
              ['kind', 'id', 'name', 'state'],
            );
            return {
              kind: string(object.kind, 'kind'),
              id: string(object.id, 'ID'),
              name: string(object.name, 'name'),
              state: string(object.state, 'state'),
              ...(typeof object.previous_name === 'string'
                ? { previous_name: object.previous_name }
                : {}),
              ...(Array.isArray(object.changes)
                ? {
                    changes: object.changes.map((entry) => {
                      const change = record(
                        entry,
                        ['field', 'operation', 'before', 'after', 'unit', 'truncated'],
                        'related change',
                        ['field', 'operation'],
                      );
                      return {
                        field: string(change.field, 'field'),
                        operation: string(change.operation, 'operation'),
                        ...(typeof change.before === 'string' ? { before: change.before } : {}),
                        ...(typeof change.after === 'string' ? { after: change.after } : {}),
                        ...(typeof change.unit === 'string' ? { unit: change.unit } : {}),
                        ...(change.truncated === true ? { truncated: true } : {}),
                      };
                    }),
                  }
                : {}),
            };
          }),
        }
      : {}),
    ...(Array.isArray(row.impacts)
      ? {
          impacts: row.impacts.map((value) => {
            const impact = record(
              value,
              ['kind', 'created', 'updated', 'deleted', 'sampled', 'omitted'],
              'impact',
              ['kind', 'created', 'updated', 'deleted'],
            );
            return {
              kind: string(impact.kind, 'kind'),
              created: decimal(impact.created, 'created'),
              updated: decimal(impact.updated, 'updated'),
              deleted: decimal(impact.deleted, 'deleted'),
              sampled: typeof impact.sampled === 'string' ? impact.sampled : '0',
              omitted: typeof impact.omitted === 'string' ? impact.omitted : '0',
            };
          }),
        }
      : {}),
  };
}
function cleanupJob(value: unknown): CleanupJob {
  const row = record(
    value,
    [
      'id',
      'state',
      'created_at',
      'updated_at',
      'actor',
      'filter',
      'cutoff_max_id',
      'deleted_operations',
      'deleted_items',
      'skipped_active',
      'error_code',
    ],
    'cleanup progress',
  );
  return {
    id: decimal(row.id, 'cleanup ID'),
    state: string(row.state, 'cleanup state'),
    created_at: integer(row.created_at, 'cleanup time'),
    updated_at: integer(row.updated_at, 'cleanup update'),
    actor: subject(row.actor),
    filter: row.filter as Filter,
    cutoff_max_id: decimal(row.cutoff_max_id, 'cleanup cutoff'),
    deleted_operations: decimal(row.deleted_operations, 'deleted operations'),
    deleted_items: decimal(row.deleted_items, 'deleted targets'),
    skipped_active: decimal(row.skipped_active, 'active operations'),
    error_code: string(row.error_code, 'cleanup error'),
  };
}
export async function getHistory(
  filter: Filter,
  view: string,
  page: string,
  size: PageSize,
  snapshot = '',
  signal?: AbortSignal,
) {
  return historyPage(
    await apiFetch(
      queryPath(base, {
        ...filter,
        view,
        page,
        page_size: size,
        snapshot_max_id: snapshot || undefined,
      }),
      { signal },
    ),
  );
}
export async function getOperation(id: string, signal?: AbortSignal) {
  return operation(await apiFetch(base + '/' + encodeURIComponent(id), { signal }));
}
export async function getOperationItems(
  id: string,
  page: string,
  size: PageSize,
  signal?: AbortSignal,
) {
  return itemPage(
    await apiFetch(
      queryPath(base + '/' + encodeURIComponent(id) + '/items', { page, page_size: size }),
      { signal },
    ),
  );
}
export async function prepareCleanup(
  filter: Filter,
  signal?: AbortSignal,
): Promise<PreparedCleanup> {
  const value = await apiFetch<unknown>(base + '/cleanup/prepare', {
    method: 'POST',
    json: { filter, all: Object.keys(filter).length === 0 },
    signal,
  });
  const row = record(value, ['filter', 'all', 'cutoff_max_id'], 'cleanup scope');
  return {
    filter,
    all: Object.keys(filter).length === 0,
    cutoff_max_id: decimal(row.cutoff_max_id, 'cleanup cutoff'),
  };
}
export async function startCleanup(
  prepared: PreparedCleanup,
  requestID: string,
  signal?: AbortSignal,
) {
  return cleanupJob(
    await apiFetch(base + '/cleanup', {
      method: 'POST',
      json: { prepared, request_id: requestID },
      signal,
    }),
  );
}
export async function getCleanup(id: string, signal?: AbortSignal) {
  return cleanupJob(
    await apiFetch(base + '/cleanup/' + encodeURIComponent(id) + '/status', { signal }),
  );
}
export async function resumeCleanup(id: string, signal?: AbortSignal) {
  await apiFetch(base + '/cleanup/' + encodeURIComponent(id) + '/resume', {
    method: 'POST',
    signal,
  });
}
