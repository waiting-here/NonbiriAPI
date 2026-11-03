import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ToastViewport } from './Toast';

afterEach(() => vi.useRealTimers());
describe('toast reading time', () => {
  it('allows five seconds and preserves remaining time while hovered or focused', () => {
    vi.useFakeTimers();
    const dismiss = vi.fn();
    render(<ToastViewport toasts={[{ id: 'one', message: 'Saved' }]} onDismiss={dismiss} />);
    const toast = screen.getByRole('status');
    act(() => vi.advanceTimersByTime(4_000));
    expect(dismiss).not.toHaveBeenCalled();
    fireEvent.pointerEnter(toast);
    act(() => vi.advanceTimersByTime(10_000));
    fireEvent.focus(screen.getByRole('button'));
    fireEvent.pointerLeave(toast);
    act(() => vi.advanceTimersByTime(10_000));
    expect(dismiss).not.toHaveBeenCalled();
    fireEvent.blur(screen.getByRole('button'), { relatedTarget: document.body });
    act(() => vi.advanceTimersByTime(999));
    expect(dismiss).not.toHaveBeenCalled();
    act(() => vi.advanceTimersByTime(1));
    expect(dismiss).toHaveBeenCalledExactlyOnceWith('one');
  });
  it('keeps errors until explicit acknowledgement and avoids nested live regions', () => {
    vi.useFakeTimers();
    const dismiss = vi.fn();
    render(
      <ToastViewport
        toasts={[{ id: 'error', message: 'Failed', tone: 'error' }]}
        onDismiss={dismiss}
      />,
    );
    act(() => vi.advanceTimersByTime(60_000));
    expect(dismiss).not.toHaveBeenCalled();
    expect(screen.getByRole('alert').parentElement).not.toHaveAttribute('aria-live');
    fireEvent.click(screen.getByRole('button'));
    expect(dismiss).toHaveBeenCalledExactlyOnceWith('error');
  });
});
