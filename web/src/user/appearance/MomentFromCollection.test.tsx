import { screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { AppearanceContext, type AppearanceState } from './context';
import { MomentFromCollection } from './MomentFromCollection';

const key = 'card:l-chatgpt';
const owned: AppearanceState = {
  selected: 'default',
  unlocked: ['default', 'heart-moment'],
  moments: [key],
  updated_at: null,
};

function control(state: AppearanceState, select = vi.fn().mockResolvedValue(undefined)) {
  return (
    <AppearanceContext
      value={{ state, select, saving: false, pending: false, refresh: vi.fn(), error: null }}
    >
      <MomentFromCollection artworkKey={key} />
    </AppearanceContext>
  );
}

it('applies an owned card directly and honours the current appearance and ownership', async () => {
  const select = vi.fn().mockResolvedValue(undefined);
  const view = await renderWithProviders(control(owned, select), { station: 'user', role: 'user' });
  await view.user.click(screen.getByRole('button', { name: 'Set as site background' }));
  expect(select).toHaveBeenCalledWith('heart-moment', key);
  view.rerender(control({ ...owned, selected: 'heart-moment', moment: key }));
  expect(screen.getByRole('button')).toBeDisabled();
  view.rerender(control({ ...owned, moments: [] }));
  expect(screen.queryByRole('button')).toBeNull();
});
