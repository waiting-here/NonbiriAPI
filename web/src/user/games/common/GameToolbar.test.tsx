import { useState } from 'react';
import { screen, waitFor, within } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { GameToolbar } from './GameToolbar';

it('keeps sound and music independent and returns keyboard focus to their menu trigger', async () => {
  function Example() {
    const [sound, setSound] = useState(true),
      [music, setMusic] = useState(false);
    return (
      <GameToolbar
        items={[]}
        sound={{ enabled: sound, toggle: () => setSound(!sound) }}
        music={{ enabled: music, toggle: () => setMusic(!music) }}
      />
    );
  }
  const view = await renderWithProviders(<Example />, { station: 'user' });
  const trigger = screen.getByRole('button', { name: 'Sound' });
  await view.user.click(trigger);
  const sound = screen.getByRole('menuitemcheckbox', { name: 'Sound: on' });
  await waitFor(() => expect(sound).toHaveFocus());
  expect(sound).toHaveAttribute('aria-checked', 'true');
  await view.user.keyboard('{Enter}');
  expect(trigger).toHaveFocus();
  await view.user.click(trigger);
  expect(screen.getByRole('menuitemcheckbox', { name: 'Sound: off' })).toHaveAttribute(
    'aria-checked',
    'false',
  );
  expect(screen.getByRole('menuitemcheckbox', { name: 'Music: off' })).toHaveAttribute(
    'aria-checked',
    'false',
  );
  await view.user.keyboard('{ArrowDown}{Enter}');
  await view.user.click(trigger);
  expect(screen.getByRole('menuitemcheckbox', { name: 'Sound: off' })).toHaveAttribute(
    'aria-checked',
    'false',
  );
  expect(screen.getByRole('menuitemcheckbox', { name: 'Music: on' })).toHaveAttribute(
    'aria-checked',
    'true',
  );
  await view.user.keyboard('{Escape}');
  expect(trigger).toHaveFocus();
});

it('orders main tools and keeps extra links and disabled actions in input order', async () => {
  const disabled = vi.fn();
  const view = await renderWithProviders(
    <GameToolbar
      items={[
        { id: 'catalog', label: 'Catalog', icon: 'book', disabled: true, onClick: disabled },
        { id: 'rankings', label: 'Rankings', icon: 'trophy', href: '#game-rankings' },
        { id: 'rules', label: 'Rules', icon: 'help', onClick: vi.fn() },
        { id: 'account', label: 'Account', icon: 'account', to: '/account' },
        { id: 'history', label: 'History', icon: 'history', onClick: vi.fn() },
      ]}
    />,
    { station: 'user' },
  );
  const group = screen.getByRole('group', { name: 'Game tools' });
  expect(
    [...group.querySelectorAll('.game-toolbar__items > button, .game-toolbar__items > a')].map(
      (node) => node.textContent,
    ),
  ).toEqual(['Rules', 'History', 'Rankings']);
  const more = screen.getByRole('button', { name: 'More' });
  await view.user.click(more);
  const menu = screen.getByRole('menu', { name: 'More' });
  const catalog = within(menu).getByRole('menuitem', { name: 'Catalog' }),
    account = within(menu).getByRole('menuitem', { name: 'Account' });
  expect(catalog).toBeDisabled();
  expect(account).toHaveAttribute('href', '/account');
  await waitFor(() => expect(account).toHaveFocus());
  expect(disabled).not.toHaveBeenCalled();
  await view.user.keyboard('{Escape}');
  expect(more).toHaveFocus();
});
