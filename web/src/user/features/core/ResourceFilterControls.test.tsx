import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import { useLocation, useNavigate } from 'react-router';
import { describe, expect, it } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { useUrlPagePager } from '@shared/operations/useUrlPagePager';
import { clearResourceNavigation } from '@shared/operations/resourceNavigation';
import { ResourceFilterBar } from './ResourceFilterControls';
import { useResourceFilters } from './useResourceFilters';

function Harness({ account }: { account: string }) {
  const filter = useResourceFilters('models', account);
  const pager = useUrlPagePager({
    station: 'user',
    listType: 'models',
    scopeKey: account,
    resetKey: filter.identity,
  });
  const location = useLocation();
  const navigate = useNavigate();
  return (
    <>
      <ResourceFilterBar control={filter} />
      <output data-testid="filters">{filter.identity}</output>
      <output data-testid="page">{pager.page}</output>
      <output data-testid="url">{location.search}</output>
      <button onClick={() => pager.setPage('2')}>Second page</button>
      <button onClick={() => navigate(-1)}>Back</button>
    </>
  );
}

describe('resource search navigation', () => {
  it('keeps a submitted search when a select changes before the route renders', async () => {
    await renderWithProviders(<Harness account="first" />, {
      station: 'user',
      route: '/models?page=2&page_size=10',
    });
    fireEvent.change(screen.getByRole('searchbox'), { target: { value: 'Needle' } });
    act(() => {
      fireEvent.submit(screen.getByRole('form', { name: 'Resource filters' }));
      fireEvent.change(screen.getByRole('combobox', { name: 'Connections' }), {
        target: { value: 'available' },
      });
    });
    await waitFor(() =>
      expect(screen.getByTestId('filters')).toHaveTextContent(
        'q=Needle&connection_state=available',
      ),
    );
    expect(screen.getByTestId('page')).toHaveTextContent('1');
  });

  it('combines committed filters, resets pages, restores POP, and drops old-account filters', async () => {
    const view = await renderWithProviders(<Harness account="first" />, {
      station: 'user',
      route: '/models?page=3&page_size=10&q=Needle&provider=Vendor',
    });
    expect(screen.getByTestId('page')).toHaveTextContent('3');
    await view.user.selectOptions(
      screen.getByRole('combobox', { name: 'Connections' }),
      'available',
    );
    expect(screen.getByTestId('filters')).toHaveTextContent(
      'q=Needle&provider=Vendor&connection_state=available',
    );
    expect(screen.getByTestId('page')).toHaveTextContent('1');
    await view.user.click(screen.getByRole('button', { name: 'Second page' }));
    expect(screen.getByTestId('page')).toHaveTextContent('2');
    await view.user.click(screen.getByRole('button', { name: 'Clear filters' }));
    expect(screen.getByTestId('filters')).toBeEmptyDOMElement();
    await view.user.click(screen.getByRole('button', { name: 'Back' }));
    expect(screen.getByTestId('filters')).toHaveTextContent('q=Needle');
    expect(screen.getByTestId('page')).toHaveTextContent('2');
    view.rerender(<Harness account="second" />);
    expect(screen.getByTestId('filters')).toBeEmptyDOMElement();
    await waitFor(() => expect(screen.getByTestId('url')).not.toHaveTextContent('Needle'));
    expect(screen.getByTestId('page')).toHaveTextContent('1');
    await view.user.click(screen.getByRole('button', { name: 'Back' }));
    expect(screen.getByTestId('filters')).toBeEmptyDOMElement();
  });

  it('clears the same account after logout and rejects overlong or control-character searches', async () => {
    const view = await renderWithProviders(<Harness account="same" />, {
      station: 'user',
      route: '/models?page=2&q=PrivateFilter',
    });
    await waitFor(() => expect(screen.getByTestId('filters')).toHaveTextContent('PrivateFilter'));
    act(() => clearResourceNavigation(view.queryClient));
    view.rerender(<Harness account="same" />);
    expect(screen.getByTestId('filters')).toBeEmptyDOMElement();
    const input = screen.getByRole('searchbox', { name: 'Search' });
    fireEvent.change(input, { target: { value: 'a'.repeat(513) } });
    await view.user.click(screen.getByRole('button', { name: 'Search' }));
    expect(screen.getByRole('alert')).toHaveTextContent('Check the search text');
    expect(screen.getByTestId('filters')).toBeEmptyDOMElement();
  });
});
