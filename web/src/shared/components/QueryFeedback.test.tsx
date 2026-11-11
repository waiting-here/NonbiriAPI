import { act, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { ApiError } from '@shared/query/http';
import { QueryFeedback } from './QueryFeedback';
import { ErrorState, LoadingState } from './States';

afterEach(() => vi.useRealTimers());

it('announces retained results immediately and remains explicit during a long query', async () => {
  const view = await renderWithProviders(
    <QueryFeedback query={{ isFetching: false, data: [] }} />,
    { station: 'admin' },
  );
  vi.useFakeTimers();
  view.rerender(<QueryFeedback query={{ isFetching: true, data: [] }} />);
  expect(screen.getByRole('status')).toHaveTextContent('Updating results');
  expect(screen.getByRole('status')).toHaveTextContent('previous results');
  act(() => vi.advanceTimersByTime(8_000));
  expect(screen.getByRole('status')).toHaveTextContent('still running');
  view.rerender(<QueryFeedback query={{ isFetching: false, data: [] }} />);
  expect(screen.queryByRole('status')).toBeNull();
  view.rerender(<QueryFeedback query={{ isFetching: true, data: [] }} />);
  expect(screen.getByRole('status')).toHaveTextContent('Updating results');
});

it('keeps polling quiet while announcing changed-page placeholders', async () => {
  const view = await renderWithProviders(
    <QueryFeedback query={{ isFetching: true, data: [] }} quietRefetch />,
    { station: 'user' },
  );
  expect(screen.queryByRole('status')).toBeNull();
  view.rerender(
    <QueryFeedback query={{ isFetching: true, data: [], isPlaceholderData: true }} quietRefetch />,
  );
  expect(screen.getByRole('status')).toHaveTextContent('Updating results');
});

it('continues to explain an initial long load', async () => {
  const view = await renderWithProviders(<span />, { station: 'user' });
  vi.useFakeTimers();
  view.rerender(<LoadingState />);
  expect(screen.getByRole('status')).toHaveTextContent('Loading');
  act(() => vi.advanceTimersByTime(8_000));
  expect(screen.getByRole('status')).toHaveTextContent('still running');
});

it('localizes insufficient funds without replacing genuine permission errors', async () => {
  const view = await renderWithProviders(
    <ErrorState error={new ApiError('insufficient_credits', 'opaque server message', 403)} />,
    { station: 'user' },
  );
  expect(screen.getByRole('alert')).toHaveTextContent('not enough available balance');
  expect(screen.getByRole('alert')).not.toHaveTextContent('not allowed');
  view.rerender(<ErrorState error={new ApiError('forbidden', 'opaque server message', 403)} />);
  expect(screen.getByRole('alert')).toHaveTextContent('not allowed');
});

it('identifies the previous result after a failed replacement query', async () => {
  await renderWithProviders(
    <QueryFeedback
      query={{
        isFetching: false,
        data: ['saved result'],
        error: new ApiError('service_unavailable', 'Timed out', 503),
        retained: true,
      }}
    />,
    { station: 'user' },
  );
  expect(screen.getByRole('status')).toHaveTextContent('query failed');
  expect(screen.getByRole('status')).toHaveTextContent('previous results');
});
