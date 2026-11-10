import { screen } from '@testing-library/react';
import { expect, it } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { ApiError } from '@shared/query/http';
import { CoreErrorPanel } from './components';

it('preserves a query-unavailable reason and only uses maintenance copy for the maintenance code', async () => {
  const view = await renderWithProviders(
    <CoreErrorPanel
      error={new ApiError('service_unavailable', 'The query timed out. Retry the search.', 503)}
    />,
    { station: 'user' },
  );
  expect(screen.getByRole('alert')).toHaveTextContent('The query timed out. Retry the search.');
  view.rerender(
    <CoreErrorPanel error={new ApiError('maintenance', 'Opaque server message', 503)} />,
  );
  expect(screen.getByRole('alert')).toHaveTextContent('This operation is temporarily unavailable.');
  expect(screen.getByRole('alert')).not.toHaveTextContent('Opaque server message');
});

it('distinguishes a business 403 from a forbidden operation', async () => {
  const view = await renderWithProviders(
    <CoreErrorPanel error={new ApiError('insufficient_credits', 'Opaque server message', 403)} />,
    { station: 'user' },
  );
  expect(screen.getByRole('alert')).toHaveTextContent('not enough available balance');
  view.rerender(<CoreErrorPanel error={new ApiError('forbidden', 'Opaque server message', 403)} />);
  expect(screen.getByRole('alert')).toHaveTextContent(
    'Your current session no longer permits this operation.',
  );
});
