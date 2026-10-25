import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { DonationShareFields } from './DonationShareFields';
describe('donation authorization material', () => {
  it('shows the exact credential version without sub-service choices', async () => {
    await renderWithProviders(
      <DonationShareFields
        choice={{
          key: { nativeServices: ['chat_completions', 'responses'], credentialVersion: '1' },
        }}
      />,
      { station: 'user', role: 'user' },
    );
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
    expect(screen.getByText(/version 1/)).toBeVisible();
  });
});
