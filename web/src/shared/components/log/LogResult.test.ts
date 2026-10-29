import { describe, expect, it } from 'vitest';
import { logResult, logResultCopyKey } from './LogResult';
import type { RoleLogRow } from './data';
const failure = {
  route_kind: 'openai_chat_completions',
  caller_result_class: 'failed',
  caller_status: 503,
  caller_error_code: 'upstream',
  phase: 'handler',
  rejection_reason: null,
} satisfies Parameters<typeof logResult>[0];
describe('caller-safe log result presentation', () => {
  it('distinguishes accepted discovery tasks from successful model calls', () => {
    const accepted = { ...failure, caller_result_class: 'success' as const, caller_status: 202 };
    expect(logResult({ ...accepted, route_kind: 'model_discovery' })).toMatchObject({
      key: 'discoveryAccepted',
      tone: 'info',
      hint: 'discoveryAcceptedHint',
    });
    expect(logResult(accepted).key).toBe('success');
    expect(logResult({ ...accepted, route_kind: 'model_discovery', caller_status: 200 }).key).toBe(
      'success',
    );
    expect(logResult({ ...failure, route_kind: 'model_discovery' }).key).toBe('upstream');
    expect(
      logResult({ ...accepted, route_kind: 'model_discovery', caller_result_class: 'cancelled' })
        .key,
    ).toBe('cancelled');
    for (const role of ['user', 'admin', 'steward'] as const)
      expect(logResultCopyKey(role, 'discoveryAccepted')).toBe(
        'common.operations.logs.discoveryAccepted',
      );
  });
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
  it.each(['handler', 'pre_handler'] as const)(
    'distinguishes missing models from unavailable bindings in %s',
    (phase) => {
      for (const [code, status, key] of [
        ['not_found', 404, 'missingModel'],
        ['unbound_model', 503, 'noUsableKey'],
      ] as const) {
        expect(
          logResult({ ...failure, phase, caller_status: status, caller_error_code: code }).key,
        ).toBe(key);
        expect(
          logResult({
            ...failure,
            phase,
            caller_status: status,
            caller_error_code: null,
            rejection_reason: code,
          }).key,
        ).toBe(key);
      }
    },
  );
  it.each(['user', 'admin', 'steward'] as const)('uses the %s translation namespace', (role) => {
    for (const key of ['missingModel', 'noUsableKey'] as const) {
      expect(logResultCopyKey(role, key)).toBe(
        role === 'user'
          ? 'user.logs.result.' + key
          : 'common.operations.logs.presentation.result.' + key,
      );
    }
  });
  it('keeps specific admission reasons while distinguishing cancellation and pending calls', () => {
    expect(
      logResult({ ...failure, phase: 'pre_handler', caller_error_code: 'insufficient_credits' })
        .key,
    ).toBe('credits');
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
