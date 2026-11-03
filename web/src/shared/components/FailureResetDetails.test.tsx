import { screen } from '@testing-library/react';
import { expect, it } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { DisplayTimeContext } from './timeContextValue';
import { ServiceHealth } from './ResourceHealth';
import { FailureResetDetails } from './FailureResetDetails';
import { formatDateTime } from '@shared/utils/datetime';

it('formats health and reset receipts with the same Beijing station context in a New York browser', async () => {
  const at = Date.parse('2026-11-04T06:41:00Z') / 1000;
  const context = { mode: 'site', offset_minutes: 480 } as const;
  const expected = formatDateTime(at, 'en', context);
  await renderWithProviders(
    <DisplayTimeContext.Provider value={context}>
      <ServiceHealth
        statuses={[
          {
            use_id: '1',
            native_service: 'chat_completions',
            state: 'rate_limited',
            failure_streak: '0',
            failure_disabled: false,
            retry_after_until: at,
            smart_backoff: null,
          },
        ]}
      />
      <FailureResetDetails
        services={[
          {
            use_id: '1',
            native_service: 'chat_completions',
            status: 'already_clear',
            failure_streak_before: '0',
            failure_disabled_before: false,
            retry_after_until: at,
            backoff_retry_at: at,
            manually_disabled: false,
          },
        ]}
      />
    </DisplayTimeContext.Provider>,
    { station: 'user', locale: 'en' },
  );
  expect(screen.getAllByText((content) => content.includes(expected))).toHaveLength(2);
  expect(expected).toContain('02:41 PM');
  if (process.env.TZ === 'America/New_York') expect(formatDateTime(at, 'en')).toContain('01:41 AM');
});
