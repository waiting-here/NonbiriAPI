import { describe, expect, test, vi } from 'vitest';
import { screen } from '@testing-library/react';
import { AccountMenu } from '../../src/shared/components/AccountMenu';
import { LanguageSwitcher } from '../../src/shared/components/LanguageSwitcher';
import { renderWithProviders } from './support';

describe('AccountMenu keyboard contract', () => {
  test('uses a non-modal dialog, focuses its first control, traps Tab, and restores focus on Escape', async () => {
    const rendered = await renderWithProviders(
      <AccountMenu displayName="fixture-user" signOutLabel="Sign out" onSignOut={vi.fn()} />,
      { station: 'user', role: 'user' },
    );
    const trigger = screen.getByRole('button', { name: 'fixture-user' });
    await rendered.user.click(trigger);

    expect(screen.getByRole('dialog')).toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: 'Theme' })).toHaveFocus();
    await rendered.user.tab();
    await rendered.user.tab();
    await rendered.user.tab();
    expect(screen.getByRole('combobox', { name: 'Theme' })).toHaveFocus();

    await rendered.user.keyboard('{Escape}');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
  });

  test('uses the station registry for the administrator settings destination', async () => {
    const rendered = await renderWithProviders(
      <AccountMenu
        displayName="fixture-admin"
        signOutLabel="Sign out"
        station="admin"
        languageControl={<LanguageSwitcher />}
        onSignOut={vi.fn()}
      />,
      { station: 'admin', role: 'admin' },
    );
    await rendered.user.click(screen.getByRole('button', { name: 'fixture-admin' }));
    expect(screen.getByRole('link', { name: 'Site settings' })).toHaveAttribute('href', '/settings');
    expect(screen.getByRole('group', { name: 'Language' })).toBeInTheDocument();
  });
});
