import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import {
  normalizeBindingAvailability,
  normalizeServiceStatuses,
  type ServiceStatus,
} from '@shared/operations/resourceHealth';
import { BindingHealth, ServiceHealth } from './ResourceHealth';

const service: ServiceStatus = {
  use_id: '1',
  native_service: 'chat_completions',
  state: 'failure_disabled',
  failure_streak: '10',
  failure_disabled: true,
  retry_after_until: null,
  smart_backoff: null,
};

describe('current service health', () => {
  it('shows a Google model quota wait only for the affected binding service', async () => {
    const waiting = {
      ...service,
      native_service: 'gemini_generate_content',
      state: 'rate_limited',
      failure_streak: '0',
      failure_disabled: false,
      retry_after_until: 1_800_000_000,
      model_quota_reason: 'google_minute_input_tokens',
    };
    const value = normalizeBindingAvailability({
      state: 'available',
      service_statuses: [
        waiting,
        {
          ...waiting,
          use_id: '2',
          native_service: 'embeddings',
          state: 'available',
          model_quota_reason: undefined,
          retry_after_until: null,
        },
      ],
    });
    await renderWithProviders(<BindingHealth value={value} />, { station: 'admin' });
    expect(screen.getByText('1 of 2 services healthy')).toBeVisible();
    expect(screen.getByText('Google model input token quota per minute exhausted')).toBeVisible();
    expect(screen.getByText(/Rate limit wait until/)).toBeVisible();
    expect(normalizeServiceStatuses([waiting])[0]).not.toHaveProperty('model_quota_reason');
  });

  it('makes a partial service pause and manual recovery visible without claiming the whole key is unavailable', async () => {
    const statuses = normalizeServiceStatuses([
      service,
      {
        ...service,
        use_id: '2',
        native_service: 'embeddings',
        state: 'available',
        failure_streak: '0',
        failure_disabled: false,
      },
    ]);
    await renderWithProviders(<ServiceHealth statuses={statuses} />, { station: 'admin' });
    expect(screen.getByText('1 of 2 services healthy')).toBeVisible();
    expect(screen.getByText('Paused after consecutive failures')).toBeVisible();
    expect(screen.getByText('10 consecutive failures')).toBeVisible();
    expect(screen.getByText(/Once the upstream recovers, reset failures/)).toBeVisible();
    expect(screen.queryByText('Currently unavailable')).not.toBeInTheDocument();
  });

  it('distinguishes a recovery probe from manual suspension and avoids a fabricated deadline', async () => {
    await renderWithProviders(
      <BindingHealth
        value={{
          state: 'unavailable',
          service_statuses: [
            {
              ...service,
              state: 'rate_limited',
              failure_disabled: false,
              failure_streak: '0',
              smart_backoff: {
                state: 'probing',
                reason: 'quota',
                source: 'builtin',
                retry_at: 1_800_000_000,
              },
            },
          ],
        }}
      />,
      { station: 'user' },
    );
    expect(screen.getByText(/Checking whether the upstream has recovered/)).toBeVisible();
    expect(screen.queryByText(/Rate limit wait until/)).not.toBeInTheDocument();
    expect(
      screen.queryByText(/Once the upstream recovers, reset failures/),
    ).not.toBeInTheDocument();
  });

  it('shows an actionable fallback when another resource condition blocks healthy services', async () => {
    await renderWithProviders(
      <BindingHealth
        value={{
          state: 'unavailable',
          service_statuses: [
            service,
            { ...service, state: 'available', failure_streak: '0', failure_disabled: false },
          ],
        }}
      />,
      { station: 'admin' },
    );
    expect(screen.getByText('Currently unavailable')).toBeVisible();
    expect(screen.getByText(/Check the linked donation’s authorization/)).toBeVisible();
  });

  it('does not promise a trial request while authorization still requires review', async () => {
    await renderWithProviders(
      <ServiceHealth
        statuses={[
          {
            ...service,
            state: 'review_required',
            failure_disabled: false,
            failure_streak: '0',
            smart_backoff: {
              state: 'ready',
              reason: 'quota',
              source: 'builtin',
              retry_at: 1_800_000_000,
            },
          },
        ]}
      />,
      { station: 'admin' },
    );
    expect(screen.getByText('The current credential needs a new review before use.')).toBeVisible();
    expect(
      screen.queryByText('The next request will check whether the upstream has recovered.'),
    ).not.toBeInTheDocument();
  });
});
