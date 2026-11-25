import { screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { installJsonFetchFixtures, renderWithProviders } from '../../../../test/unit/support';
import type { CompanionGame, CompanionPrograms } from '@shared/companion';
import { CompanionPage } from './CompanionPage';
import { CompanionsPage } from '../../../admin/pages/CompanionsPage';

vi.mock('./LikesBoard', () => ({ LikesBoard: () => null }));
vi.mock('../gwent/GwentGame', () => ({ GwentGame: () => null }));

const games = [
  ['bidding', 'Bidding Duel'],
  ['likes', 'Turn-based battle'],
  ['gwent', 'AI Gwent'],
] as const;
const stale: CompanionPrograms = {
  enabled: true,
  upload_available: true,
  upload_global_enabled: true,
  upload_game_enabled: true,
  official: {
    name: 'official',
    version: 0,
    difficulties: [1],
    versions: [{ version: 1, status: 'ready', created_at: 11 }],
  },
  programs: [
    {
      name: 'Old private opponent',
      version: 1,
      versions: [{ version: 1, status: 'ready', created_at: 10 }],
    },
  ],
};
const home = { active: null, latest: null, preferences: { memory: 'none', contribute: false } };
const context = {
  accepting: true,
  wallets: { balance: '0', gameBalance: '0' },
  config: { available: true, enabled: true, modes: {} },
};

it.each(games)(
  'offers only the built-in opponent in %s even with stale upload metadata',
  async (game: CompanionGame) => {
    const base = '/api/games/' + game + '/companion';
    installJsonFetchFixtures([
      { method: 'GET', path: base, body: home },
      { method: 'GET', path: base + '/programs', body: stale },
    ]);
    const view = await renderWithProviders(<CompanionPage game={game} context={context} />, {
      station: 'user',
    });
    expect(await screen.findByRole('heading', { name: 'Official companion' })).toBeVisible();
    expect(screen.getByRole('slider', { name: 'Difficulty 1' })).toBeVisible();
    expect(screen.queryByRole('combobox', { name: 'Opponent' })).toBeNull();
    expect(screen.queryByText('My private programs')).toBeNull();
    expect(view.container.querySelector('input[type=file],textarea,a[download]')).toBeNull();
    expect(view.container).not.toHaveTextContent(
      /Old private opponent|upload|compiler diagnostics/i,
    );
    expect(screen.getByText('Memory and sharing')).toBeVisible();
    expect(screen.getByRole('button', { name: 'Companion history' })).toBeVisible();
  },
);

it('keeps both administrative upload gates and all program controls visibly disabled on every game', async () => {
  installJsonFetchFixtures(
    games.map(([game]) => ({
      method: 'GET',
      path: '/admin/api/games/' + game + '/companion',
      body: stale,
    })),
  );
  const { user, container } = await renderWithProviders(<CompanionsPage />, { station: 'admin' });
  for (const [, label] of games) {
    await user.click(screen.getByRole('tab', { name: label }));
    expect(
      await screen.findByRole('switch', { name: 'Allow new companion matches' }),
    ).toBeChecked();
    for (const name of ['Site-wide program uploads', 'Program uploads for this game']) {
      const toggle = screen.getByRole('switch', { name });
      expect(toggle).toBeDisabled();
      expect(toggle).not.toBeChecked();
    }
    expect(screen.getByText('Program uploads are unavailable in v1.4.3.')).toBeVisible();
    expect(container.querySelector('input[type=file]')).toBeDisabled();
    expect(screen.getByRole('textbox', { name: 'Source code' })).toBeDisabled();
    expect(screen.getByRole('combobox', { name: 'Official program version' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Activate version' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Upload and compile' })).toBeDisabled();
    expect(container).not.toHaveTextContent('Old private opponent');
  }
});
