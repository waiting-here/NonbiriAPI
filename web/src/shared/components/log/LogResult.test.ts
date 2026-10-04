import { describe, expect, it } from 'vitest';
import { logResult } from './LogResult';
import type { RoleLogRow } from './data';
const failure = {
  caller_result_class: 'failed',
  caller_status: 503,
  caller_error_code: 'upstream',
  phase: 'handler',
  rejection_reason: null,
} satisfies Parameters<typeof logResult>[0];
describe('caller-safe log result presentation', () => {
  it.each([
    [401, 'keyRejected'],
    [403, 'keyRejected'],
    [429, 'rateLimited'],
    [408, 'timeout'],
    [504, 'timeout'],
    [503, 'upstream'],
  ] as const)('explains provider status %i with %s', (status, key) => {
    expect(logResult({ ...failure, caller_status: status }).key).toBe(key);
  });
  it('does not mistake platform authorization or unknown errors for provider key errors', () => {
    expect(
      logResult({ ...failure, caller_status: 401, caller_error_code: 'unauthorized' }).key,
    ).toBe('failed');
    expect(
      logResult({ ...failure, caller_status: 503, caller_error_code: 'service_unavailable' }).key,
    ).toBe('failed');
  });
  it('keeps specific admission reasons while distinguishing cancellation and pending calls', () => {
    expect(
      logResult({ ...failure, phase: 'pre_handler', caller_error_code: 'insufficient_credits' })
        .key,
    ).toBe('credits');
    expect(
      logResult({ ...failure, phase: 'pre_handler', caller_error_code: 'unbound_model' }).key,
    ).toBe('missingModel');
    expect(
      logResult({ ...failure, phase: 'pre_handler', caller_error_code: 'concurrency' }).key,
    ).toBe('rejected');
    for (const [value, key] of [
      ['cancelled', 'cancelled'],
      [null, 'pending'],
      ['success', 'success'],
    ] as const)
      expect(
        logResult({ ...failure, caller_result_class: value as RoleLogRow['caller_result_class'] })
          .key,
      ).toBe(key);
  });
});
