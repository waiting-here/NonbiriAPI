import { integer, oneOf } from './wire';

export type KeyOverflow = 'reject' | 'queue';

export interface KeyQueueConfig {
  concurrency_overflow: KeyOverflow;
  rpm_overflow: KeyOverflow;
  concurrency_queue_limit: number;
  rpm_queue_limit: number;
}

export type KeyQueueProjection = {
  [K in keyof KeyQueueConfig]?: KeyQueueConfig[K] | null;
};

export const keyQueueFields = [
  'concurrency_overflow',
  'rpm_overflow',
  'concurrency_queue_limit',
  'rpm_queue_limit',
] as const;

export interface KeyQueueDraft {
  concurrency_overflow: KeyOverflow;
  rpm_overflow: KeyOverflow;
  concurrency_queue_limit: string;
  rpm_queue_limit: string;
}

export const keyQueueDraft = (value: KeyQueueProjection = {}): KeyQueueDraft => ({
  concurrency_overflow: value.concurrency_overflow ?? 'reject',
  rpm_overflow: value.rpm_overflow ?? 'reject',
  concurrency_queue_limit: String(value.concurrency_queue_limit ?? 0),
  rpm_queue_limit: String(value.rpm_queue_limit ?? 0),
});

export const keyQueueInput = (value: KeyQueueDraft): KeyQueueConfig => ({
  ...value,
  concurrency_queue_limit: Number(value.concurrency_queue_limit),
  rpm_queue_limit: Number(value.rpm_queue_limit),
});

export function keyQueueChanges(
  value: KeyQueueDraft,
  baseline: KeyQueueProjection,
): Partial<KeyQueueConfig> {
  const next = keyQueueInput(value);
  const previous = keyQueueInput(keyQueueDraft(baseline));
  return Object.fromEntries(
    keyQueueFields
      .filter((field) => next[field] !== previous[field])
      .map((field) => [field, next[field]]),
  );
}

export function normalizeKeyQueue(root: Record<string, unknown>): KeyQueueConfig {
  return {
    concurrency_overflow:
      root.concurrency_overflow === undefined
        ? 'reject'
        : oneOf(root.concurrency_overflow, ['reject', 'queue'], 'key concurrency overflow'),
    rpm_overflow:
      root.rpm_overflow === undefined
        ? 'reject'
        : oneOf(root.rpm_overflow, ['reject', 'queue'], 'key RPM overflow'),
    concurrency_queue_limit:
      root.concurrency_queue_limit === undefined
        ? 0
        : integer(root.concurrency_queue_limit, 'key concurrency queue limit', 0, 2_147_483_647),
    rpm_queue_limit:
      root.rpm_queue_limit === undefined
        ? 0
        : integer(root.rpm_queue_limit, 'key RPM queue limit', 0, 2_147_483_647),
  };
}

export function normalizeManagedKeyQueue(root: Record<string, unknown>): KeyQueueProjection {
  return root.max_concurrency == null || root.max_rpm == null
    ? {
        concurrency_overflow: null,
        rpm_overflow: null,
        concurrency_queue_limit: null,
        rpm_queue_limit: null,
      }
    : normalizeKeyQueue(root);
}

export function keyQueuePatch(root: Record<string, unknown>): Partial<KeyQueueConfig> {
  const config = normalizeKeyQueue(root);
  return Object.fromEntries(
    keyQueueFields
      .filter((field) => root[field] !== undefined)
      .map((field) => [field, config[field]]),
  );
}
