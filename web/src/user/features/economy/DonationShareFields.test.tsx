import { useState } from 'react';
import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { DonationShareFields, type DonationShare } from './DonationShareFields';

function Draft() {
  const [share, setShare] = useState<DonationShare>({
    services: ['chat_completions', 'responses'],
  });
  return (
    <DonationShareFields
      choice={{
        key: { nativeServices: ['chat_completions', 'responses'], credentialVersion: '1' },
      }}
      value={share}
      disabled={false}
      onChange={setShare}
    />
  );
}
describe('donation service choices', () => {
  it('lets donors choose services without a separate catalog permission', async () => {
    const view = await renderWithProviders(<Draft />, { station: 'user', role: 'user' });
    expect(screen.getAllByRole('checkbox')).toHaveLength(2);
    expect(
      screen.getByText(/Administrators and authorized stewards can fetch its model list/),
    ).toBeVisible();
    await view.user.click(screen.getByRole('checkbox', { name: 'Responses' }));
    expect(screen.getByRole('checkbox', { name: 'Chat completions' })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'Responses' })).not.toBeChecked();
  });
});
