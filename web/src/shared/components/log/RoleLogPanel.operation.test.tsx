import { renderHistoricalLogs } from '../../../../test/unit/logSupport';
import { screen, waitFor } from '@testing-library/react';
import { useLocation, useNavigate } from 'react-router';
import { describe, expect, it } from 'vitest';
import { installJsonFetchFixtures } from '../../../../test/unit/support';
import { RoleLogPanel } from './RoleLogPanel';

function Navigation() {
  const location = useLocation();
  const navigate = useNavigate();
  return (
    <>
      <output data-testid="query">{location.search}</output>
      <button onClick={() => navigate(-1)}>Back</button>
    </>
  );
}
describe('request service type', () => {
  it.each(['user', 'admin', 'steward'] as const)(
    'binds %s filters, export and browser history to operation',
    async (role) => {
      const prefix = role === 'admin' ? '/admin/api' : '/api';
      const root = role === 'steward' ? prefix + '/steward' : prefix;
      const body = {
        data: [],
        next_cursor: null,
        pagination: { page: '1', page_size: 20, total_items: '0', total_pages: '0' },
      };
      const fetchMock = installJsonFetchFixtures([
        { method: 'GET', path: prefix + '/time-zones', body: { version: 'test', zones: ['UTC'] } },
        ...['chat_completions', 'images_generations', ''].map((operation) => ({
          method: 'GET' as const,
          path:
            root +
            '/logs?' +
            (operation ? 'operation=' + operation + '&' : '') +
            'page=1&page_size=20',
          body,
        })),
      ]);
      const view = await renderHistoricalLogs(
        <>
          <RoleLogPanel role={role} accountId="viewer" />
          <Navigation />
        </>,
        {
          station: role === 'admin' ? 'admin' : 'user',
          role: role === 'steward' ? 'level5' : role,
          route: '/logs?operation=chat_completions',
        },
      );
      const select = screen.getByRole('combobox', { name: 'Service type' });
      expect(select).toHaveValue('chat_completions');
      expect(select.querySelectorAll('option')).toHaveLength(18);
      await view.user.selectOptions(select, 'images_generations');
      await waitFor(() =>
        expect(screen.getByTestId('query')).toHaveTextContent('operation=images_generations'),
      );
      await waitFor(() =>
        expect(
          fetchMock.mock.calls.some(([url]) =>
            String(url).includes('operation=images_generations'),
          ),
        ).toBe(true),
      );
      const exportLink = view.container.querySelector<HTMLAnchorElement>(
        'a[download][href*="export.csv"]',
      );
      expect(exportLink).not.toBeNull();
      expect(new URL(exportLink!.href).searchParams.get('operation')).toBe('images_generations');
      await view.user.selectOptions(select, '');
      await waitFor(() => expect(screen.getByTestId('query')).not.toHaveTextContent('operation='));
      expect(view.container.querySelector('a[download][href*="export.csv"]')).not.toHaveAttribute(
        'href',
        expect.stringContaining('operation='),
      );
      await view.user.click(screen.getByRole('button', { name: /^Back$/ }));
      await waitFor(() => expect(select).toHaveValue('images_generations'));
    },
  );
});
