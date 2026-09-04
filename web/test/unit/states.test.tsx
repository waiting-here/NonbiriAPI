import { describe, expect, test } from 'vitest';
import { ApiError } from '../../src/shared/query/http';
import { ErrorState } from '../../src/shared/components/States';
import { renderWithProviders } from './support';

describe('shared error state', () => {
  test('does not expose diagnostic wire fields unless an authorized detail is supplied', async () => {
    const diagnostic = 'upstream-internal-diagnostic-marker';
    const rendered = await renderWithProviders(
      <ErrorState error={new ApiError('internal', 'Safe public message.', 500, { diag: diagnostic })} />,
      { station: 'user' },
    );

    expect(rendered.queryByText(diagnostic)).not.toBeInTheDocument();
    expect(rendered.queryByText('Safe public message.')).toBeInTheDocument();
  });
});
