import { screen, waitFor } from '@testing-library/react';
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
              smart_backoff_profile: 'none' as const,
              smart_backoff_rules: [],
              smart_backoff: null,
              failure_streak: '12',
              failure_disabled: false,
              revision: '8',
            }),
            { status: 200, headers: { 'Content-Type': 'application/json' } },
          );
        }),
      );
      const props = {
        role,
        donationID: '1',
        keyID: '2',
        revision: '7',
        threshold: '10',
        profile: 'none' as const,
        rules: [],
      };
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
      expect(screen.queryByText('Failure policy saved and recalculated.')).not.toBeInTheDocument();
      expect(screen.getByText(/Never disable automatically for errors/)).toBeInTheDocument();
    });
  }
  it('retains rules through profile changes and retries unknown saves with the same request', async () => {
    const requests: { body: unknown; key: string | null }[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_path, options) => {
        const body = JSON.parse(String(options?.body));
        requests.push({ body, key: new Headers(options?.headers).get('Idempotency-Key') });
        if (requests.length === 1) throw new TypeError('response lost');
        return new Response(
          JSON.stringify({
            donation_id: '1',
            donation_key_id: '2',
            failure_disable_threshold: '10',
            smart_backoff_profile: body.smart_backoff_profile,
            smart_backoff_rules: body.smart_backoff_rules,
            smart_backoff: null,
            failure_streak: '3',
            failure_disabled: false,
            revision: '8',
          }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        );
      }),
    );
    const view = await renderWithProviders(
      <FailurePolicyControl
        role="owner"
        donationID="1"
        keyID="2"
        revision="7"
        threshold="10"
        profile="none"
        rules={[]}
        refresh={async () => undefined}
      />,
      { station: 'user', locale: 'en' },
    );
    view.queryClient.setQueryData(['user', 'session'], {
      user: { id: '3', username: 'fixture', effective_level: 1 },
    });
    const profile = screen.getByLabelText('Smart quota backoff');
    await view.user.selectOptions(profile, 'day_month');
    await view.user.click(screen.getByText('Additional error recognition (optional)'));
    await view.user.click(screen.getByRole('button', { name: 'Add rule' }));
    await view.user.selectOptions(screen.getByLabelText('Rule 1 type'), 'message');
    await view.user.type(screen.getByLabelText('Rule 1 value'), '  monthly allowance exhausted  ');
    await view.user.selectOptions(screen.getByLabelText('Rule 1 allowance cycle'), 'month');
    await view.user.selectOptions(profile, 'day');
    expect(screen.getByRole('button', { name: 'Save failure policy' })).toBeDisabled();
    expect(screen.getByLabelText('Rule 1 value')).toHaveValue('  monthly allowance exhausted  ');
    await view.user.selectOptions(profile, 'day_month');
    await view.user.click(screen.getByRole('button', { name: 'Save failure policy' }));
    await waitFor(() => expect(profile).toBeDisabled());
    await view.user.click(await screen.findByRole('button', { name: 'Continue' }));
    await screen.findByText('Failure policy saved and recalculated.');
    expect(requests).toHaveLength(2);
    expect(requests[1]).toEqual(requests[0]);
    expect(requests[0].body).toEqual({
      expected_revision: '7',
      failure_disable_threshold: '10',
      smart_backoff_profile: 'day_month',
      smart_backoff_rules: [
        { kind: 'message', value: 'monthly allowance exhausted', period: 'month' },
      ],
    });
    expect(requests[0].key).toMatch(/^[A-Za-z0-9_-]{22,128}$/);
  });

  it('only rereads after a confirmed save whose refresh failed', async () => {
    const refresh = vi
      .fn()
      .mockRejectedValueOnce(new Error('read unavailable'))
      .mockResolvedValue(undefined);
    const fetchMock = vi.fn(
      async () =>
        new Response(
          JSON.stringify({
            donation_id: '1',
            donation_key_id: '2',
            failure_disable_threshold: '10',
            smart_backoff_profile: 'day',
            smart_backoff_rules: [],
            smart_backoff: null,
            failure_streak: '3',
            failure_disabled: false,
            revision: '8',
          }),
          { status: 200, headers: { 'Content-Type': 'application/json' } },
        ),
    );
    vi.stubGlobal('fetch', fetchMock);
    const view = await renderWithProviders(
      <FailurePolicyControl
        role="admin"
        donationID="1"
        keyID="2"
        revision="7"
        threshold="10"
        profile="none"
        rules={[]}
        refresh={refresh}
      />,
      { station: 'admin', locale: 'en' },
    );
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture' } });
    await view.user.selectOptions(screen.getByLabelText('Smart quota backoff'), 'day');
    await view.user.click(screen.getByRole('button', { name: 'Save failure policy' }));
    await screen.findByText('Saved, but the latest version could not be loaded.');
    await view.user.click(screen.getAllByRole('button', { name: 'Check again' })[0]);
    await screen.findByText('Failure policy saved and recalculated.');
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(refresh).toHaveBeenCalledTimes(2);
    expect(screen.getByLabelText('Smart quota backoff')).toHaveValue('day');
  });
});
