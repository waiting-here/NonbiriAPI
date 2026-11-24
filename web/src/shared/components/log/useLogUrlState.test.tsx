import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { StrictMode } from 'react';
import { createMemoryRouter, RouterProvider, useLocation, useNavigate } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useLogUrlState, type LogUrlState } from './useLogUrlState';

const fields = ['model', 'operation'] as const;
const now = 1_800_000_000;

function mount(search = '', scope = '') {
  const observations: LogUrlState[] = [];
  function Probe() {
    const { state, patch } = useLogUrlState(fields, 20, scope);
    const location = useLocation();
    const navigate = useNavigate();
    observations.push(state);
    return (
      <>
        <output data-testid="query">{location.search}</output>
        <output data-testid="range">{JSON.stringify(state)}</output>
        <button onClick={() => patch({ fromUnix: undefined, toUnix: undefined, page: 1 })}>
          Clear
        </button>
        <button onClick={() => patch({ fromUnix: now - 3_600, toUnix: now, page: 1 })}>
          Set range
        </button>
        <button
          onClick={() =>
            patch({
              filters: { model: 'private model', operation: 'chat_completions' },
              range: '15m',
              fromUnix: undefined,
              toUnix: undefined,
            })
          }
        >
          Use private filter
        </button>
        <button onClick={() => navigate(-1)}>Back</button>
        <button onClick={() => navigate(1)}>Forward</button>
      </>
    );
  }
  const router = createMemoryRouter(
    [
      { path: '/before', element: <p>Before logs</p> },
      { path: '/logs', element: <Probe /> },
    ],
    { initialEntries: ['/before', `/logs${search}`], initialIndex: 1 },
  );
  render(
    <StrictMode>
      <RouterProvider router={router} />
    </StrictMode>,
  );
  return { router, observations };
}

const query = () => new URLSearchParams(screen.getByTestId('query').textContent ?? '');
afterEach(() => vi.restoreAllMocks());

describe('log time range', () => {
  it('keeps a private model in the current role history and only shares the rolling preset and operation', async () => {
    const { router } = mount('', 'user:7');
    fireEvent.click(screen.getByRole('button', { name: 'Use private filter' }));
    await waitFor(() => expect(query().get('range')).toBe('15m'));
    expect(query().has('model')).toBe(false);
    expect(query().has('from')).toBe(false);
    expect(query().get('operation')).toBe('chat_completions');
    expect(router.state.location.state?.logPrivate).toEqual({
      scope: 'user:7',
      filters: { model: 'private model' },
    });
    expect(JSON.parse(screen.getByTestId('range').textContent!).filters.model).toBe(
      'private model',
    );
  });
  it.each(['images_generations', 'chat_completions', 'model_discovery'])(
    'preserves service operation %s through range updates and history',
    async (operation) => {
      mount('?operation=' + operation + '&page=3&anchor=keep');
      expect(JSON.parse(screen.getByTestId('range').textContent!).filters.operation).toBe(
        operation,
      );
      fireEvent.click(screen.getByRole('button', { name: 'Set range' }));
      expect(query().get('operation')).toBe(operation);
      expect(query().get('page')).toBe('1');
      expect(query().get('anchor')).toBe('keep');
      fireEvent.click(screen.getByRole('button', { name: 'Back' }));
      await waitFor(() => expect(query().get('page')).toBe('3'));
      expect(query().get('operation')).toBe(operation);
    },
  );
  it.each([
    '',
    'unknown',
    'CHAT_COMPLETIONS',
    '%20chat_completions',
    'chat_completions&operation=chat_completions',
  ])('ignores malformed operation from URL: %s', (value) => {
    mount('?operation=' + value);
    expect(JSON.parse(screen.getByTestId('range').textContent!).filters).not.toHaveProperty(
      'operation',
    );
    fireEvent.click(screen.getByRole('button', { name: 'Clear' }));
    expect(query().has('operation')).toBe(false);
  });

  it('leaves the initial request unbounded when the URL has no time range', () => {
    const { observations } = mount('?model=demo&page=3&anchor=keep');
    expect(query().has('from')).toBe(false);
    expect(query().has('to')).toBe(false);
    expect(query().get('model')).toBe('demo');
    expect(query().get('page')).toBe('3');
    expect(query().get('anchor')).toBe('keep');
    expect(
      observations.every((state) => state.fromUnix === undefined && state.toUnix === undefined),
    ).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: 'Back' }));
    expect(screen.getByText('Before logs')).toBeVisible();
  });

  it.each(['?from=123&to=456', '?from=123', '?to=456', '?from=&to='])(
    'preserves an explicit range %s',
    async (search) => {
      mount(search);
      expect(screen.getByTestId('query')).toHaveTextContent(search);
      expect(JSON.parse(screen.getByTestId('range').textContent!)).toMatchObject({
        ...(search.includes('from=123') ? { fromUnix: 123 } : {}),
        ...(search.includes('to=456') ? { toUnix: 456 } : {}),
      });
    },
  );

  it('keeps a cleared range through browser back and forward', async () => {
    vi.spyOn(Date, 'now').mockReturnValue(now * 1_000);
    mount();
    expect(query().has('from')).toBe(false);
    expect(query().has('to')).toBe(false);
    fireEvent.click(screen.getByRole('button', { name: 'Set range' }));
    await waitFor(() => expect(query().get('to')).toBe(String(now)));
    expect(query().get('from')).toBe(String(now - 3_600));
    fireEvent.click(screen.getByRole('button', { name: 'Clear' }));
    await waitFor(() => expect(query().has('from')).toBe(false));
    expect(query().has('to')).toBe(false);
    expect(JSON.parse(screen.getByTestId('range').textContent!)).not.toHaveProperty('fromUnix');
    fireEvent.click(screen.getByRole('button', { name: 'Back' }));
    await waitFor(() => expect(query().get('from')).toBe(String(now - 3_600)));
    expect(query().get('to')).toBe(String(now));
    fireEvent.click(screen.getByRole('button', { name: 'Forward' }));
    await waitFor(() => expect(query().has('from')).toBe(false));
    expect(query().has('to')).toBe(false);
  });

  it('keeps an ignored reversed link range out of the applied filters', () => {
    mount('?from=456&to=123&model=shared');
    const state = JSON.parse(screen.getByTestId('range').textContent!);
    expect(state.filters.model).toBe('shared');
    expect(state).not.toHaveProperty('fromUnix');
    expect(state).not.toHaveProperty('toUnix');
    expect(query().get('from')).toBe('456');
  });
});
