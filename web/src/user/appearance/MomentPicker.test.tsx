import { screen, within } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { AppearanceContext } from './context';
import { defaultMoment, momentArtworks } from './moments';
import { MomentPicker } from './MomentPicker';
import en from '../i18n/en.json';

it('submits moment search explicitly and resets filters without changing the chosen artwork', async () => {
  const card = momentArtworks.find((item) => item.kind === 'card')!;
  const select = vi.fn();
  const copy = en.user.appearance.moments;
  const view = await renderWithProviders(
    <AppearanceContext
      value={{
        state: {
          selected: 'heart-moment',
          unlocked: ['heart-moment'],
          moment: card.key,
          moments: [defaultMoment.key, card.key],
          updated_at: null,
        },
        pending: false,
        error: null,
        saving: false,
        select,
        refresh: vi.fn(),
      }}
    >
      <MomentPicker />
    </AppearanceContext>,
    { station: 'user', role: 'user' },
  );
  const choices = within(screen.getByRole('radiogroup', { name: copy.choose }));
  expect(choices.getAllByRole('radio')).toHaveLength(2);
  const search = screen.getByRole('textbox', { name: copy.search });
  await view.user.type(search, 'no matching artwork');
  expect(choices.getAllByRole('radio')).toHaveLength(2);
  await view.user.keyboard('{Enter}');
  expect(choices.queryAllByRole('radio')).toHaveLength(0);
  await view.user.click(screen.getByRole('button', { name: 'Reset' }));
  expect(search).toHaveValue('');
  expect(choices.getAllByRole('radio')).toHaveLength(2);
  expect(choices.getByRole('radio', { checked: true })).toHaveAttribute('value', card.key);
  await view.user.type(search, defaultMoment.nameEN);
  await view.user.click(screen.getByRole('button', { name: 'Search', exact: true }));
  expect(choices.getAllByRole('radio')).toHaveLength(1);
  expect(choices.getByRole('radio')).toHaveAttribute('value', defaultMoment.key);
  expect(select).not.toHaveBeenCalled();
});
