import { act, fireEvent, screen } from '@testing-library/react';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { ToastProvider, TOAST_DEFAULT_DURATIONS, useToast } from '../../src/shared/components/Toast';
import { renderWithProviders } from './support';

function ToastProbe() {
  const { push, clear } = useToast();
  return (
    <div>
      <button type="button" onClick={() => push({ message: 'Info message', tone: 'info' })}>Push info</button>
      <button type="button" onClick={() => push({ message: 'Warning message', tone: 'warning' })}>Push warning</button>
      <button type="button" onClick={() => push({ message: 'Error message', tone: 'error' })}>Push error</button>
      <button type="button" onClick={() => push({ message: 'Custom message', tone: 'success', durationMs: 1_000 })}>Push custom</button>
      <button type="button" onClick={clear}>Clear toasts</button>
    </div>
  );
}

afterEach(() => {
  vi.useRealTimers();
});

describe('toast severity and lifetime policy', () => {
  test('expires ordinary feedback according to tone defaults', async () => {
    vi.useFakeTimers();
    const rendered = await renderWithProviders(
      <ToastProvider><ToastProbe /></ToastProvider>,
      { station: 'user' },
    );
    fireEvent.click(screen.getByRole('button', { name: 'Push info' }));
    expect(screen.getByRole('status')).toHaveTextContent('Info message');
    act(() => vi.advanceTimersByTime(TOAST_DEFAULT_DURATIONS.info - 1));
    expect(screen.getByRole('status')).toBeInTheDocument();
    act(() => vi.advanceTimersByTime(1));
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
    rendered.unmount();
  });

  test('keeps errors persistent, warning longer, and supports an explicit override', async () => {
    vi.useFakeTimers();
    const rendered = await renderWithProviders(
      <ToastProvider><ToastProbe /></ToastProvider>,
      { station: 'user' },
    );
    fireEvent.click(screen.getByRole('button', { name: 'Push custom' }));
    act(() => vi.advanceTimersByTime(999));
    expect(screen.getByText('Custom message')).toBeInTheDocument();
    act(() => vi.advanceTimersByTime(1));
    expect(screen.queryByText('Custom message')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Push warning' }));
    fireEvent.click(screen.getByRole('button', { name: 'Push error' }));
    expect(screen.getByRole('alert')).toHaveTextContent('Error message');
    act(() => vi.advanceTimersByTime(TOAST_DEFAULT_DURATIONS.info));
    expect(screen.getByText('Warning message')).toBeInTheDocument();
    expect(screen.getByRole('alert')).toBeInTheDocument();
    act(() => vi.advanceTimersByTime(TOAST_DEFAULT_DURATIONS.warning - TOAST_DEFAULT_DURATIONS.info));
    expect(screen.queryByText('Warning message')).not.toBeInTheDocument();
    expect(screen.getByRole('alert')).toBeInTheDocument();
    rendered.unmount();
  });

  test('cleans timers when the station provider unmounts', async () => {
    vi.useFakeTimers();
    const rendered = await renderWithProviders(
      <ToastProvider><ToastProbe /></ToastProvider>,
      { station: 'user' },
    );
    fireEvent.click(screen.getByRole('button', { name: 'Push info' }));
    expect(vi.getTimerCount()).toBeGreaterThan(0);
    rendered.unmount();
    expect(vi.getTimerCount()).toBe(0);
  });

  test('clears prior-account feedback at an explicit station boundary', async () => {
    vi.useFakeTimers();
    const rendered = await renderWithProviders(
      <ToastProvider><ToastProbe /></ToastProvider>,
      { station: 'user' },
    );
    fireEvent.click(screen.getByRole('button', { name: 'Push error' }));
    expect(screen.getByRole('alert')).toHaveTextContent('Error message');
    fireEvent.click(screen.getByRole('button', { name: 'Clear toasts' }));
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(vi.getTimerCount()).toBe(0);
    rendered.unmount();
  });
});
