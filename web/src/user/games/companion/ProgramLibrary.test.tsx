import { screen, waitFor, within } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { renderWithProviders, installJsonFetchFixtures } from '../../../../test/unit/support';
import type { CompanionPrograms } from '@shared/companion';
import { ProgramLibrary } from './ProgramLibrary';

it('keeps a failed revision inspectable and lets the owner restore an older usable version', async () => {
  const base = '/api/games/bidding/companion/programs';
  const programs: CompanionPrograms = {
    enabled: true,
    upload_available: true,
    official: { name: 'official', version: 0, difficulties: [1] },
    programs: [
      {
        name: 'My bidder',
        version: 2,
        versions: [
          { version: 3, status: 'failed', created_at: 10, diagnostic: 'missing return value' },
          { version: 2, status: 'ready', created_at: 8 },
          { version: 1, status: 'ready', created_at: 7 },
        ],
      },
    ],
  };
  const fetch = installJsonFetchFixtures([
    {
      method: 'GET',
      path: base + '?name=My%20bidder&version=3',
      body: { source: 'int play() {}' },
    },
    {
      method: 'POST',
      path: base,
      body: { name: 'My bidder', version: 3, status: 'failed', diagnostic: 'missing return value' },
    },
    { method: 'POST', path: base + '/activate', body: programs },
    { method: 'POST', path: base + '/delete', body: { ...programs, programs: [] } },
  ]);
  const refresh = vi.fn(async () => {});
  const { user, queryClient } = await renderWithProviders(
    <ProgramLibrary game="bidding" programs={programs} refresh={refresh} />,
    { station: 'user' },
  );
  queryClient.setQueryData(['user', 'session'], {
    user: { id: '7', username: 'companion-player', effective_level: 1 },
  });
  const uploads = screen.getByRole('button', { name: 'Upload and compile' });
  await user.type(screen.getByRole('combobox', { name: 'Program name' }), 'My bidder');
  // Code contains braces, so enter it as literal text.
  await user.click(screen.getByRole('textbox', { name: 'Source code' }));
  await user.paste('int play() {}');
  await user.click(uploads);
  await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Compilation failed'));
  expect(fetch.mock.calls.map((call) => call[1]?.body)).toContain(
    JSON.stringify({ name: 'My bidder', source: 'int play() {}' }),
  );
  await user.click(screen.getByText('Compiler diagnostics'));
  expect(screen.getByText('missing return value')).toBeVisible();
  await user.click(screen.getAllByRole('button', { name: 'View source' })[0]);
  expect(await screen.findByText('int play() {}', { selector: 'pre' })).toBeVisible();
  await user.click(screen.getByRole('button', { name: 'Activate version' }));
  await waitFor(() => expect(refresh).toHaveBeenCalledTimes(2));
  expect(fetch.mock.calls.map((call) => call[1]?.body)).toContain(
    JSON.stringify({ version: 1, name: 'My bidder' }),
  );
  await user.click(screen.getByRole('button', { name: 'Delete program' }));
  await user.click(
    within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Delete program' }),
  );
  await waitFor(() => expect(refresh).toHaveBeenCalledTimes(3));
});
