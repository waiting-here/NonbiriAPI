import { act, screen, waitFor, within } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { ClientModelField } from './ClientModelField';

const browserChunk = vi.hoisted(() => {
  let release!: () => void;
  const pending = new Promise<void>((resolve) => {
    release = resolve;
  });
  return { pending, release, requested: vi.fn() };
});
vi.mock('./ClientModelBrowser', async (original) => {
  browserChunk.requested();
  await browserChunk.pending;
  return original();
});

it('loads the open source only and keeps the chosen model and search across pages and visits', async () => {
  const calls: URL[] = [];
  const names = Array.from({ length: 12 }, (_, i) => 'embed-' + (i + 1));
  names.push("reader's-v2");
  let failCharity = true;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: string) => {
      const url = new URL(input, window.location.origin);
      calls.push(url);
      if (url.pathname === '/api/charity/models') {
        expect(url.searchParams.get('allowed_for_me')).toBe('true');
        expect(url.searchParams.get('currently_available')).toBe('true');
        if (failCharity)
          return Response.json(
            { error: { code: 'internal', message: 'Catalog unavailable' } },
            { status: 500 },
          );
        return Response.json({
          models: [],
          donation_intake: 'open',
          server_now: 1800000000,
          pagination: { page: '1', page_size: 10, total_items: '0', total_pages: '1' },
        });
      }
      expect(url.pathname).toBe('/api/models');
      expect(url.searchParams.get('connection_state')).toBe('available');
      const q = url.searchParams.get('q') ?? '';
      const filtered = names.filter((name) => ('mine/' + name).includes(q));
      const page = Number(url.searchParams.get('page'));
      const size = Number(url.searchParams.get('page_size'));
      return Response.json({
        data: filtered.slice((page - 1) * size, page * size).map((name) => ({
          id: String(names.indexOf(name) + 1),
          provider: 'mine',
          model: name,
          full_name: 'mine/' + name,
          model_types: ['embeddings'],
          route_strategy: 'ordered',
          silent_retry: false,
          transport_rule: 'passthrough',
          flatten_tool_calls: false,
          cloudflare_proxy_mode: 'legacy',
          cloudflare_proxy_enabled: false,
          revision: '1',
          binding_revision: '1',
          binding_count: '1',
          created_at: 1700000000,
          updated_at: 1700000000,
        })),
        next_cursor: null,
        pagination: {
          page: String(page),
          page_size: size,
          total_items: String(filtered.length),
          total_pages: String(Math.max(1, Math.ceil(filtered.length / size))),
        },
      });
    }),
  );
  const view = await renderWithProviders(<ClientModelField accountId="1" />, { station: 'user' });
  expect(calls).toHaveLength(0);
  const opener = screen.getByRole('button', { name: 'Choose a model' });
  expect(browserChunk.requested).not.toHaveBeenCalled();
  await act(async () => {
    await view.user.click(opener);
  });
  expect(await screen.findByRole('status')).toHaveTextContent('Loading');
  await view.user.click(screen.getByRole('button', { name: 'Close' }));
  await act(async () => {
    browserChunk.release();
  });
  await waitFor(() => expect(opener).toHaveFocus());
  expect(calls).toHaveLength(0);
  await act(async () => {
    await view.user.click(opener);
  });
  const dialog = within(screen.getByRole('dialog'));
  const error = within(await dialog.findByRole('alert'));
  failCharity = false;
  await view.user.click(error.getByRole('button'));
  await dialog.findByText('No models available');
  expect(calls.map((url) => url.pathname)).toEqual(['/api/charity/models', '/api/charity/models']);
  await view.user.click(dialog.getByRole('tab', { name: 'My models' }));
  expect(await dialog.findByText('mine/embed-10')).toBeVisible();
  await view.user.click(dialog.getByRole('button', { name: 'Next' }));
  const target = "mine/reader's-v2";
  expect(await dialog.findByText(target)).toBeVisible();
  await waitFor(() =>
    expect(dialog.getByRole('heading', { name: 'Available models' })).toHaveFocus(),
  );
  await view.user.selectOptions(dialog.getByRole('combobox', { name: 'Items per page' }), '20');
  expect(await dialog.findByText('mine/embed-1')).toBeVisible();
  const search = dialog.getByRole('searchbox', { name: 'Search models' });
  await view.user.type(search, 'missing{Enter}');
  await dialog.findByText('No matching models');
  await view.user.clear(search);
  await view.user.type(search, "reader's{Enter}");
  const result = await dialog.findByRole('button', { name: 'Choose ' + target });
  await view.user.click(dialog.getByRole('button', { name: 'Copy ' + target }));
  expect(await navigator.clipboard.readText()).toBe(target);
  expect(screen.getByRole('dialog')).toBeVisible();
  await view.user.click(within(result).getByText(target));
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(screen.getByText(target)).toBeVisible();
  await view.user.click(screen.getByText('Supported APIs'));
  expect(screen.getByText('/v1/embeddings')).toBeVisible();
  expect(screen.queryByText('/v1/chat/completions')).not.toBeInTheDocument();
  const body = screen
    .getByRole('button', { name: 'Copy Example JSON body' })
    .parentElement!.querySelector('code')!;
  expect(JSON.parse(body.textContent!)).toEqual({
    model: target,
    input: 'How does a comet differ from an asteroid?',
  });
  const callsBefore = calls.length;
  await view.queryClient.invalidateQueries();
  expect(calls).toHaveLength(callsBefore);
  const change = screen.getByRole('button', { name: 'Change model' });
  await view.user.click(change);
  expect(screen.getByRole('tab', { name: 'My models' })).toHaveAttribute('aria-selected', 'true');
  expect(await screen.findByRole('searchbox')).toHaveValue("reader's");
  await view.user.keyboard('{Escape}');
  await waitFor(() => expect(change).toHaveFocus());
});
