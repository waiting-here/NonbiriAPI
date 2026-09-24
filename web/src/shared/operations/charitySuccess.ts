import { boolean, integer, invalidResponse, record, unixSecond } from './wire';

export interface CharitySuccess {
  window_start: number;
  as_of: number;
  success: number;
  failure: number;
  cancelled: number;
  sample_count: number;
  rate: number | null;
  insufficient_sample: boolean;
  capture_started_at: number;
}

export function normalizeCharitySuccess(value: unknown): CharitySuccess {
  const row = record(
    value,
    [
      'window_start',
      'as_of',
      'success',
      'failure',
      'cancelled',
      'sample_count',
      'rate',
      'insufficient_sample',
      'capture_started_at',
    ],
    'charity success rate',
  );
  const result: CharitySuccess = {
    window_start: unixSecond(row.window_start, 'success window'),
    as_of: unixSecond(row.as_of, 'success snapshot'),
    success: integer(row.success, 'success count'),
    failure: integer(row.failure, 'failure count'),
    cancelled: integer(row.cancelled, 'cancelled count'),
    sample_count: integer(row.sample_count, 'sample count'),
    rate:
      row.rate === null
        ? null
        : typeof row.rate === 'number' &&
            Number.isFinite(row.rate) &&
            row.rate >= 0 &&
            row.rate <= 1
          ? row.rate
          : invalidResponse('success rate'),
    insufficient_sample: boolean(row.insufficient_sample, 'sample sufficiency'),
    capture_started_at: unixSecond(row.capture_started_at, 'success capture start'),
  };
  if (
    result.window_start > result.as_of ||
    result.success + result.failure !== result.sample_count ||
    result.insufficient_sample !== result.sample_count < 20 ||
    (result.sample_count === 0
      ? result.rate !== null
      : result.rate === null ||
        Math.abs(result.rate - result.success / result.sample_count) > 1e-12)
  )
    invalidResponse('success summary consistency');
  return result;
}
