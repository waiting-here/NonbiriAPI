import { useQuery } from '@tanstack/react-query';
import { decoded } from '@shared/operations/api';
import { array, boolean, decimalID, integer, invalidResponse, oneOf, record, string, unixSecond } from '@shared/operations/wire';
import { normalizeAdminAlert, type AdminAlert } from './core';

const TARGET_KINDS = [
  'deleted_account', 'donation', 'donation_key', 'endpoint_key', 'endpoint', 'user',
  'report_case', 'issue', 'fishing_batch', 'rps_session', 'request_log',
  'maintenance_event', 'worker_checkpoint',
] as const;

export interface AlertFact { key: string; value: string }
export interface AlertTarget {
  kind: (typeof TARGET_KINDS)[number];
  id: string;
  available: boolean;
  status: string;
  unavailable_reason?: string;
}
export interface AlertLogWindow {
  endpoint_key_id: string;
  from: number;
  to: number;
  available: boolean;
  unavailable_reason?: string;
}
export interface AdminAlertDetail {
  alert: AdminAlert;
  context_version: number;
  occurred_facts: AlertFact[];
  targets: AlertTarget[];
  current_state: AlertFact[];
  related_logs: AlertLogWindow | null;
  resolution_kind: string;
}

export function validAlertID(value: string | null): value is string {
  return value !== null && /^[1-9][0-9]{0,18}$/.test(value) && BigInt(value) <= 9_223_372_036_854_775_807n;
}

function fact(value: unknown): AlertFact {
  const fields = record(value, ['key', 'value'], 'alert fact');
  const key = string(fields.key, 'fact key', { min: 1, max: 64, ascii: true });
  if (!/^[a-z0-9_]+$/.test(key)) invalidResponse('fact key');
  return { key, value: string(fields.value, 'fact value', { max: 1024, bytes: 4096 }) };
}

function target(value: unknown): AlertTarget {
  const fields = record(value, ['kind', 'id', 'available', 'status', 'unavailable_reason'], 'alert target', ['kind', 'id', 'available', 'status']);
  const kind = oneOf(fields.kind, TARGET_KINDS, 'alert target kind');
  const id = string(fields.id, 'alert target id', { min: 1, max: 64, ascii: true });
  if (['deleted_account', 'donation', 'donation_key', 'endpoint_key', 'endpoint', 'user'].includes(kind)) {
    if (!validAlertID(id)) invalidResponse('alert target id');
  } else if (!/^[A-Za-z0-9_-]+$/.test(id)) invalidResponse('alert target id');
  const available = boolean(fields.available, 'alert target availability');
  const status = string(fields.status, 'alert target status', { max: 64, ascii: true });
  const reason = fields.unavailable_reason === undefined ? undefined : string(fields.unavailable_reason, 'target unavailable reason', { min: 1, max: 64, ascii: true });
  if (available === Boolean(reason)) invalidResponse('alert target availability');
  return { kind, id, available, status, ...(reason ? { unavailable_reason: reason } : {}) };
}

export function normalizeAdminAlertDetail(value: unknown): AdminAlertDetail {
  const fields = record(value, ['alert', 'context_version', 'occurred_facts', 'targets', 'current_state', 'related_logs', 'resolution_kind'], 'alert detail');
  const related = fields.related_logs === null ? null : (() => {
    const row = record(fields.related_logs, ['endpoint_key_id', 'from', 'to', 'available', 'unavailable_reason'], 'related logs', ['endpoint_key_id', 'from', 'to', 'available']);
    const available = boolean(row.available, 'related log availability');
    const reason = row.unavailable_reason === undefined ? undefined : string(row.unavailable_reason, 'related log unavailable reason', { min: 1, max: 64, ascii: true });
    if (available === Boolean(reason)) invalidResponse('related log availability');
    const from = unixSecond(row.from, 'related log start');
    const to = unixSecond(row.to, 'related log end');
    if (from > to) invalidResponse('related log window');
    return {
      endpoint_key_id: decimalID(row.endpoint_key_id, 'related endpoint key'), from, to, available,
      ...(reason ? { unavailable_reason: reason } : {}),
    };
  })();
  return {
    alert: normalizeAdminAlert(fields.alert),
    context_version: integer(fields.context_version, 'alert context version', 0, 1),
    occurred_facts: array(fields.occurred_facts, 'occurred facts', 32).map(fact),
    targets: array(fields.targets, 'alert targets', 8).map(target),
    current_state: array(fields.current_state, 'current state', 32).map(fact),
    related_logs: related,
    resolution_kind: oneOf(fields.resolution_kind, ['', 'manual', 'automatic_blacklist', 'worker_recovered', 'legacy'] as const, 'resolution kind'),
  };
}

export async function getAdminAlertDetail(id: string, signal?: AbortSignal): Promise<AdminAlertDetail> {
  if (!validAlertID(id)) invalidResponse('alert id');
  return decoded(`/admin/api/alerts/${id}`, normalizeAdminAlertDetail, { signal });
}

export function useAdminAlertDetail(accountID: string | undefined, id: string | null, enabled: boolean) {
  return useQuery({
    queryKey: ['admin', 'operations', 'alert-detail', accountID ?? 'none', id],
    queryFn: ({ signal }) => getAdminAlertDetail(id!, signal),
    enabled: enabled && Boolean(accountID) && validAlertID(id),
    retry: false,
  });
}
