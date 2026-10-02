import { useState } from 'react';
import { screen } from '@testing-library/react';
import { expect, it } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { PriceFields } from './PriceFields';
import type { PriceDraft } from './prices';

function Editor() {
  const [prices, setPrices] = useState<PriceDraft>({
    paper: '1',
    brush: '0',
    fallback: 'default',
    tiers: [],
    sizes: [{ width: '512', height: '512', paper: '2', brush: '0' }],
  });
  return <PriceFields model={{}} value={prices} onChange={setPrices} />;
}

it('keeps the optional price panel open after removing its last active override', async () => {
  const view = await renderWithProviders(<Editor />, { station: 'admin', role: 'admin' });
  const remove = screen.getByRole('button', { name: 'Remove size price 1' });
  const panel = remove.closest('details');
  expect(panel).toHaveAttribute('open');
  await view.user.click(remove);
  expect(panel).toHaveAttribute('open');
  await view.user.click(screen.getByText(/0 tiers, 0 exact sizes/));
  expect(panel).not.toHaveAttribute('open');
});
