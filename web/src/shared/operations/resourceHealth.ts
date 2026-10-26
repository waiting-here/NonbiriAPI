import { NATIVE_SERVICES, type NativeService } from '@shared/nativeServices';
import { normalizeSmartBackoff, type SmartBackoff } from './smartBackoff';
import { array, boolean, decimal, nullableUnixSecond, oneOf, record, string } from './wire';

export interface ServiceStatus {
  use_id: string;
  native_service: NativeService;
  state: string;
  failure_streak: string;
  failure_disabled: boolean;
  retry_after_until: number | null;
  smart_backoff: SmartBackoff | null;
}

export interface BindingAvailability {
  state: string;
  service_statuses: ServiceStatus[];
}

export function serviceHealthDeadlines(statuses: ServiceStatus[] = []): (number | null)[] {
  return statuses.flatMap((status) => [
    status.retry_after_until,
    status.smart_backoff?.state === 'waiting' ? status.smart_backoff.retry_at : null,
  ]);
}

export function normalizeServiceStatuses(value: unknown): ServiceStatus[] {
  if (value == null) return [];
  return array(value, 'service statuses').map((entry) => {
    const row = record(
      entry,
      [
        'use_id',
        'native_service',
        'state',
        'failure_streak',
        'failure_disabled',
        'retry_after_until',
        'smart_backoff',
      ],
      'service status',
    );
    return {
      use_id: decimal(row.use_id, 'service use id'),
      native_service: oneOf(row.native_service, NATIVE_SERVICES, 'native service'),
      state: string(row.state, 'service status'),
      failure_streak: decimal(row.failure_streak, 'service failure streak'),
      failure_disabled: boolean(row.failure_disabled, 'service paused after failures'),
      retry_after_until: nullableUnixSecond(row.retry_after_until, 'service retry time'),
      smart_backoff: normalizeSmartBackoff(row.smart_backoff ?? null),
    };
  });
}

export function normalizeBindingAvailability(value: unknown): BindingAvailability | undefined {
  if (value == null) return undefined;
  const row = record(value, ['state', 'service_statuses'], 'binding availability');
  return {
    state: string(row.state, 'binding availability state'),
    service_statuses: normalizeServiceStatuses(row.service_statuses),
  };
}
