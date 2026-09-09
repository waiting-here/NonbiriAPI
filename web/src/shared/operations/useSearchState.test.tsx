import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { StrictMode, useEffect } from 'react';
import { createMemoryRouter, RouterProvider, useNavigate } from 'react-router';
import { describe, expect, it } from 'vitest';
import { useSearchState } from './useSearchState';
import { useUrlPagePager } from './useUrlPagePager';

function FilterControl() {
  const [, setSearch] = useSearchState();
  return (
    <button
      onClick={() =>
        setSearch((previous) => {
          previous.set('q', 'edited');
          return previous;
        })
      }
    >
      Filter
    </button>
  );
}

function Probe({ name = 'one', nested = false }: { name?: string; nested?: boolean }) {
  const [search, setSearch] = useSearchState();
  const navigate = useNavigate();
  const pager = useUrlPagePager({ station: 'user', listType: `search-${name}`, scopeKey: name });
  const children = useUrlPagePager({
    station: 'user',
    listType: `children-${name}`,
    scopeKey: name,
    pageParam: 'children_page',
    pageSizeParam: 'children_size',
  });
  return (
    <section aria-label={name}>
      <output data-testid={name}>{search.toString()}</output>
      <FilterControl />
      <button onClick={() => pager.setPageSize(100)}>Size</button>
      <button onClick={() => pager.setPage('3')}>Page</button>
      {nested && <button onClick={() => children.setPage('4')}>Children</button>}
      <button
        onClick={() =>
          setSearch((previous) => {
            previous.set('count', String(Number(previous.get('count') ?? 0) + 1));
            return previous;
          })
        }
      >
        Increment
      </button>
      <button onClick={() => setSearch({ tab: 'other' })}>Replace</button>
      <button onClick={() => navigate('/other?q=external')}>External</button>
      <button onClick={() => navigate(-1)}>Back</button>
      <button onClick={() => navigate(1)}>Forward</button>
    </section>
  );
}

function mount(search: string, name = 'one', nested = false) {
  const router = createMemoryRouter(
    [{ path: '*', element: <Probe name={name} nested={nested} /> }],
    {
      initialEntries: [`/list?${search}`],
    },
  );
  render(
    <StrictMode>
      <RouterProvider router={router} />
    </StrictMode>,
  );
  return within(screen.getByRole('region', { name }));
}

function params(name = 'one') {
  return new URLSearchParams(screen.getByTestId(name).textContent ?? '');
}

describe('shared URL control updates', () => {
  it('does not navigate repeatedly when an effect writes an unchanged query', async () => {
    let effects = 0;
    function Normalize() {
      const [, setSearch] = useSearchState();
      useEffect(() => {
        effects++;
        // Bound a regression so the test reports a failure instead of hanging.
        if (effects < 5) setSearch((previous) => previous, { replace: true });
      }, [setSearch]);
      return <Probe />;
    }
    const router = createMemoryRouter([{ path: '*', element: <Normalize /> }], {
      initialEntries: ['/list?q=original'],
    });
    render(
      <StrictMode>
        <RouterProvider router={router} />
      </StrictMode>,
    );
    await waitFor(() => expect(params().get('q')).toBe('original'));
    expect(effects).toBeLessThanOrEqual(2);
    expect(router.state.location.key).toBe('default');
  });

  it('composes changes from separate filter and pager hooks before a transition renders', async () => {
    const view = mount('q=original&page=2&page_size=20');
    act(() => {
      fireEvent.click(view.getByRole('button', { name: 'Filter' }));
      fireEvent.click(view.getByRole('button', { name: 'Size' }));
    });
    await waitFor(() =>
      expect(Object.fromEntries(params())).toEqual({ q: 'edited', page: '1', page_size: '100' }),
    );
  });

  it('applies repeated functional edits to the most recent pending value', async () => {
    const view = mount('q=original');
    act(() => {
      fireEvent.click(view.getByRole('button', { name: 'Increment' }));
      fireEvent.click(view.getByRole('button', { name: 'Increment' }));
    });
    await waitFor(() => expect(params().get('count')).toBe('2'));
    expect(params().get('q')).toBe('original');
  });

  it('preserves explicit replacements while later edits compose with the replacement', async () => {
    const view = mount('q=original');
    act(() => {
      fireEvent.click(view.getByRole('button', { name: 'Filter' }));
      fireEvent.click(view.getByRole('button', { name: 'Replace' }));
      fireEvent.click(view.getByRole('button', { name: 'Increment' }));
    });
    await waitFor(() => expect(Object.fromEntries(params())).toEqual({ tab: 'other', count: '1' }));
  });

  it('takes the restored location as authority after external navigation and history changes', async () => {
    const view = mount('q=original');
    fireEvent.click(view.getByRole('button', { name: 'Filter' }));
    await waitFor(() => expect(params().get('q')).toBe('edited'));
    fireEvent.click(view.getByRole('button', { name: 'External' }));
    await waitFor(() => expect(params().get('q')).toBe('external'));
    fireEvent.click(view.getByRole('button', { name: 'Back' }));
    await waitFor(() => expect(params().get('q')).toBe('edited'));
    fireEvent.click(view.getByRole('button', { name: 'Forward' }));
    await waitFor(() => expect(params().get('q')).toBe('external'));
    fireEvent.click(view.getByRole('button', { name: 'Increment' }));
    await waitFor(() =>
      expect(Object.fromEntries(params())).toEqual({ q: 'external', count: '1' }),
    );
  });

  it('does not share pending parameters between independent routers with the same initial key', async () => {
    const first = mount('q=first', 'first');
    const second = mount('q=second', 'second');
    act(() => {
      fireEvent.click(first.getByRole('button', { name: 'Filter' }));
      fireEvent.click(second.getByRole('button', { name: 'Increment' }));
    });
    await waitFor(() => expect(params('second').get('count')).toBe('1'));
    expect(Object.fromEntries(params('first'))).toEqual({ q: 'edited' });
    expect(Object.fromEntries(params('second'))).toEqual({ q: 'second', count: '1' });
  });

  it('combines independent normalization effects and nested page changes', async () => {
    const view = mount('q=keep&page=bad&children_page=bad', 'nested', true);
    await waitFor(() =>
      expect(Object.fromEntries(params('nested'))).toEqual({
        q: 'keep',
        page: '1',
        children_page: '1',
      }),
    );
    act(() => {
      fireEvent.click(view.getByRole('button', { name: 'Page' }));
      fireEvent.click(view.getByRole('button', { name: 'Children' }));
    });
    await waitFor(() => expect(params('nested').get('children_page')).toBe('4'));
    expect(params('nested').get('page')).toBe('3');
    expect(params('nested').get('q')).toBe('keep');
  });

  it('retains a newly selected page size when a page is selected immediately afterward', async () => {
    const view = mount('page=1&page_size=20');
    act(() => {
      fireEvent.click(view.getByRole('button', { name: 'Size' }));
      fireEvent.click(view.getByRole('button', { name: 'Page' }));
    });
    await waitFor(() =>
      expect(Object.fromEntries(params())).toEqual({ page: '3', page_size: '100' }),
    );
  });
});
