import { useState } from 'react';
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { keyQueueDraft } from '@shared/operations/keyQueue';
import { KeyLimitFields, KeyLimitSummary } from './KeyRoutingLimits';

function LimitsForm() {
  const [queue, setQueue] = useState(() => keyQueueDraft());
  return (
    <KeyLimitFields
      concurrency="2"
      rpm="30"
      onConcurrency={() => undefined}
      onRPM={() => undefined}
      queue={queue}
      onQueue={setQueue}
    />
  );
}

describe('key queue settings', () => {
  it('starts with rejection and reveals only the selected queue limit without losing its input', async () => {
    const view = await renderWithProviders(<LimitsForm />, { station: 'user', locale: 'en' });
    expect(screen.getByLabelText('When concurrency is full')).toHaveValue('reject');
    expect(screen.getByLabelText('When RPM is full')).toHaveValue('reject');
    expect(screen.queryByLabelText('Concurrency queue limit')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('RPM queue limit')).not.toBeInTheDocument();
    await view.user.selectOptions(screen.getByLabelText('When concurrency is full'), 'queue');
    const limit = screen.getByLabelText('Concurrency queue limit');
    expect(limit).toHaveValue(0);
    expect(limit).not.toBeRequired();
    await view.user.clear(limit);
    await view.user.type(limit, '12');
    await view.user.selectOptions(screen.getByLabelText('When concurrency is full'), 'reject');
    await view.user.selectOptions(screen.getByLabelText('When concurrency is full'), 'queue');
    expect(screen.getByLabelText('Concurrency queue limit')).toHaveValue(12);
    expect(screen.queryByLabelText('RPM queue limit')).not.toBeInTheDocument();
  });

  it('shows queue settings and defaults old receipts without inventing limits for missing keys', async () => {
    const view = await renderWithProviders(
      <KeyLimitSummary concurrency={2} rpm={30} queue={{ concurrency_overflow: 'queue' }} />,
      { station: 'admin', locale: 'en' },
    );
    expect(screen.getByText('Wait in queue · Queue limit: Unlimited')).toBeVisible();
    expect(screen.getByText('Reject request')).toBeVisible();
    view.rerender(<KeyLimitSummary concurrency={null} rpm={null} />);
    expect(screen.queryByText('Reject request')).not.toBeInTheDocument();
    expect(screen.getByText('Maximum concurrency: Not available')).toBeVisible();
  });
});
