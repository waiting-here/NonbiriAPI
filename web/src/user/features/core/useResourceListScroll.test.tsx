import { act, fireEvent, screen } from '@testing-library/react';
import { useLayoutEffect } from 'react';
import { useLocation, useNavigate } from 'react-router';
import { afterEach, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { useResourceListScroll } from './useResourceFilters';

function List({ ready }: { ready: boolean }) {
  useResourceListScroll('account', ready);
  return <p>Resource list</p>;
}

function Harness({ ready = true, reorder = false, shrink = false }) {
  const location = useLocation();
  const navigate = useNavigate();
  useLayoutEffect(() => {
    if (location.pathname === '/detail' && shrink) {
      window.scrollTo({ top: 0 });
      window.dispatchEvent(new Event('scroll'));
    }
  }, [location.pathname, shrink]);
  return (
    <>
      <button onClick={() => navigate('/detail')}>Open detail</button>
      <button
        onClick={() =>
          navigate(reorder ? '/endpoints?q=Needle&page=2' : '/endpoints?page=2&q=Needle')
        }
      >
        Return to list
      </button>
      {location.pathname === '/endpoints' ? <List ready={ready} /> : null}
    </>
  );
}

afterEach(() => vi.restoreAllMocks());

function scrolling() {
  let top = 0;
  let next = 0;
  const frames = new Map<number, FrameRequestCallback>();
  vi.spyOn(window, 'scrollY', 'get').mockImplementation(() => top);
  vi.spyOn(window, 'scrollTo').mockImplementation((options: number | ScrollToOptions) => {
    if (typeof options === 'object') top = options.top ?? top;
  });
  vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => {
    frames.set(++next, callback);
    return next;
  });
  vi.spyOn(window, 'cancelAnimationFrame').mockImplementation((id) => {
    frames.delete(id);
  });
  return {
    top: () => top,
    scroll(value: number) {
      top = value;
      fireEvent.scroll(window);
    },
    flush() {
      act(() => {
        const pending = [...frames.values()];
        frames.clear();
        for (const callback of pending) callback(0);
      });
    },
  };
}

it('retries a cancelled restoration after a cached list refetch finishes', async () => {
  const scroll = scrolling();
  const view = await renderWithProviders(<Harness />, {
    station: 'user',
    route: '/endpoints?page=2&q=Needle',
  });
  scroll.scroll(5400);
  fireEvent.click(screen.getByRole('button', { name: 'Open detail' }));
  scroll.scroll(0);
  fireEvent.click(screen.getByRole('button', { name: 'Return to list' }));
  view.rerender(<Harness ready={false} />);
  view.rerender(<Harness ready />);
  scroll.flush();
  expect(scroll.top()).toBe(5400);
});

it('remembers the list before the detail layout shrinks and restores reordered filters', async () => {
  const scroll = scrolling();
  await renderWithProviders(<Harness reorder shrink />, {
    station: 'user',
    route: '/endpoints?page=2&q=Needle',
  });
  scroll.scroll(5400);
  fireEvent.click(screen.getByRole('button', { name: 'Open detail' }));
  expect(scroll.top()).toBe(0);
  fireEvent.click(screen.getByRole('button', { name: 'Return to list' }));
  scroll.flush();
  expect(scroll.top()).toBe(5400);
});
