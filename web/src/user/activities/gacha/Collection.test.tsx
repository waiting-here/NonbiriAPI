import { screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { gachaSnapshot } from '../../../../test/fixtures/gacha';
import { cards } from './catalog';
import { Collection } from './Collection';

it('submits card search explicitly, applies selects immediately, and preserves filters across tabs', async () => {
  const state = gachaSnapshot();
  state.visible_themes = ['g'];
  const visible = cards.filter((card) => card.theme === 'g');
  const first = visible[0]!;
  const view = await renderWithProviders(
    <Collection
      state={state}
      english
      busy={false}
      act={vi.fn()}
      inspect={vi.fn()}
      viewGroup={vi.fn()}
    />,
    { station: 'user', role: 'user' },
  );
  const count = () => view.container.querySelectorAll('.gacha-catalog .gacha-card').length;
  expect(count()).toBe(visible.length);
  const search = screen.getByRole('textbox', { name: 'Search' });
  await view.user.type(search, 'no matching card');
  expect(count()).toBe(visible.length);
  await view.user.click(screen.getByText('More filters', { exact: true }));
  await view.user.selectOptions(
    screen.getByRole('combobox', { name: 'Character' }),
    first.character,
  );
  expect(count()).toBe(visible.filter((card) => card.character === first.character).length);
  await view.user.keyboard('{Enter}');
  await view.user.click(screen.getByRole('button', { name: 'Search' }));
  expect(count()).toBe(0);
  await view.user.click(screen.getByRole('button', { name: 'Reset' }));
  expect(search).toHaveValue('');
  expect(screen.getByRole('combobox', { name: 'Character' })).toHaveValue('');
  expect(count()).toBe(visible.length);
  await view.user.type(search, first.name_en);
  await view.user.keyboard('{Enter}');
  expect(count()).toBe(1);
  await view.user.click(screen.getByRole('tab', { name: 'Theme memories' }));
  await view.user.click(screen.getByRole('tab', { name: 'Cards' }));
  expect(screen.getByRole('textbox', { name: 'Search' })).toHaveValue(first.name_en);
  expect(count()).toBe(1);
});
