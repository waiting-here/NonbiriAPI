import { screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { FailurePolicyControl } from './FailurePolicyControl';
import { validFailureThreshold } from '@shared/operations/failurePolicy';

describe('key failure policy', () => {
  it('accepts only canonical full-range U128 strings', () => {
    for (const value of ['0', '10', '340282366920938463463374607431768211455'])
      expect(validFailureThreshold(value)).toBe(true);
    for (const value of ['', '01', '-1', '1.0', ' 1', '340282366920938463463374607431768211456'])
      expect(validFailureThreshold(value)).toBe(false);
  });
  for (const role of ['owner', 'admin', 'steward'] as const) {
    it(`warns before and after zero is saved by ${role}, without a confirmation step`, async () => {
      const calls: { url: string; method: string; body: unknown }[] = [];
      vi.stubGlobal(
        'fetch',
        vi.fn(async (input, options) => {
          calls.push({
            url: String(input),
            method: options.method,
            body: JSON.parse(options.body),
          });
          return new Response(
            JSON.stringify({
              donation_id: '1',
              donation_key_id: '2',
              failure_disable_threshold: '0',
              failure_streak: '12',
              failure_disabled: false,
              revision: '8',
            }),
            { status: 200, headers: { 'Content-Type': 'application/json' } },
          );
        }),
      );
      const props = { role, donationID: '1', keyID: '2', revision: '7', threshold: '10' };
      const view = await renderWithProviders(<FailurePolicyControl {...props} />, {
        station: role === 'admin' ? 'admin' : 'user',
        locale: 'en',
      });
      view.queryClient.setQueryData(
        role === 'admin' ? ['admin', 'session'] : ['user', 'session'],
        role === 'admin'
          ? { admin: { username: 'fixture' } }
          : { user: { id: '3', username: 'fixture', effective_level: role === 'owner' ? 1 : 5 } },
      );
      const field = screen.getByLabelText('Consecutive failure disable threshold');
      await view.user.clear(field);
      await view.user.type(field, '0');
      expect(screen.getByText(/Never disable automatically for errors/)).toHaveClass(
        'failure-policy-warning',
      );
      await view.user.click(screen.getByRole('button', { name: 'Save failure policy' }));
      await screen.findByText('Failure policy saved and recalculated.');
      expect(calls).toEqual([
        {
          url: `${role === 'admin' ? '/admin/api' : role === 'steward' ? '/api/steward' : '/api'}/donations/1/keys/2/failure-policy`,
          method: 'PATCH',
          body: { expected_revision: '7', failure_disable_threshold: '0' },
        },
      ]);
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      view.rerender(<FailurePolicyControl {...props} threshold="0" revision="8" />);
      await view.user.clear(field);
      await view.user.type(field, '10');
      expect(screen.getByText(/Never disable automatically for errors/)).toBeInTheDocument();
    });
  }
});
