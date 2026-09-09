import { render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { useDetailNavigation } from './useDetailNavigation';

function NavigationProbe({
  selection,
  ready,
  showTrigger = true,
}: {
  selection: string;
  ready: boolean;
  showTrigger?: boolean;
}) {
  const { listRef, detailRef } = useDetailNavigation(selection, ready);
  return (
    <div ref={listRef} tabIndex={-1} data-testid="list">
      {showTrigger ? (
        <button type="button">Open</button>
      ) : null}
      <input aria-label="Other focus target" data-testid="other" />
      {selection ? (
        <div ref={detailRef} tabIndex={-1} data-testid="detail">
          Detail
        </div>
      ) : null}
    </div>
  );
}

describe('useDetailNavigation', () => {
  it('waits for a stable layout and does not recapture later renders', async () => {
    const originalScrollIntoView = HTMLElement.prototype.scrollIntoView;
    const scrollIntoView = vi.fn();
    HTMLElement.prototype.scrollIntoView = scrollIntoView;
    try {
      const view = render(<NavigationProbe selection="" ready />);
      const trigger = screen.getByRole('button', { name: 'Open' });
      trigger.focus();

      view.rerender(<NavigationProbe selection="selected" ready={false} />);
      await waitFor(() => expect(document.activeElement).toBe(trigger));
      expect(scrollIntoView).not.toHaveBeenCalled();

      view.rerender(<NavigationProbe selection="selected" ready />);
      const detail = await screen.findByTestId('detail');
      await waitFor(() => expect(document.activeElement).toBe(detail));
      expect(scrollIntoView).toHaveBeenCalledTimes(1);

      view.rerender(<NavigationProbe selection="selected" ready />);
      await waitFor(() => expect(document.activeElement).toBe(detail));
      expect(scrollIntoView).toHaveBeenCalledTimes(1);

      const other = screen.getByRole('textbox', { name: 'Other focus target' });
      other.focus();
      view.rerender(<NavigationProbe selection="selected" ready />);
      await waitFor(() => expect(document.activeElement).toBe(other));
      expect(scrollIntoView).toHaveBeenCalledTimes(1);

      view.rerender(<NavigationProbe selection="" ready />);
      await waitFor(() => expect(document.activeElement).toBe(trigger));
      expect(scrollIntoView).toHaveBeenCalledTimes(2);
    } finally {
      HTMLElement.prototype.scrollIntoView = originalScrollIntoView;
    }
  });

  it('does not steal focus after the user moves elsewhere while waiting', async () => {
    const originalScrollIntoView = HTMLElement.prototype.scrollIntoView;
    const scrollIntoView = vi.fn();
    HTMLElement.prototype.scrollIntoView = scrollIntoView;
    try {
      const view = render(<NavigationProbe selection="" ready />);
      screen.getByRole('button', { name: 'Open' }).focus();
      view.rerender(<NavigationProbe selection="selected" ready={false} />);

      const other = screen.getByRole('textbox', { name: 'Other focus target' });
      other.focus();
      view.rerender(<NavigationProbe selection="selected" ready />);
      await waitFor(() => expect(document.activeElement).toBe(other));
      expect(scrollIntoView).not.toHaveBeenCalled();
    } finally {
      HTMLElement.prototype.scrollIntoView = originalScrollIntoView;
    }
  });

  it('still navigates when the original trigger was replaced and focus fell to body', async () => {
    const originalScrollIntoView = HTMLElement.prototype.scrollIntoView;
    const scrollIntoView = vi.fn();
    HTMLElement.prototype.scrollIntoView = scrollIntoView;
    try {
      const view = render(<NavigationProbe selection="" ready />);
      screen.getByRole('button', { name: 'Open' }).focus();
      view.rerender(<NavigationProbe selection="selected" ready={false} />);
      view.rerender(
        <NavigationProbe selection="selected" ready={false} showTrigger={false} />,
      );
      document.body.focus();
      expect(document.activeElement).toBe(document.body);

      view.rerender(
        <NavigationProbe selection="selected" ready showTrigger={false} />,
      );
      const detail = await screen.findByTestId('detail');
      await waitFor(() => expect(document.activeElement).toBe(detail));
      expect(scrollIntoView).toHaveBeenCalledTimes(1);
    } finally {
      HTMLElement.prototype.scrollIntoView = originalScrollIntoView;
    }
  });

  it('respects a new focus target while the returning list is still loading', async () => {
    const originalScrollIntoView = HTMLElement.prototype.scrollIntoView;
    const scrollIntoView = vi.fn();
    HTMLElement.prototype.scrollIntoView = scrollIntoView;
    try {
      const view = render(<NavigationProbe selection="" ready />);
      screen.getByRole('button', { name: 'Open' }).focus();
      view.rerender(<NavigationProbe selection="selected" ready />);
      await waitFor(() => expect(document.activeElement).toBe(screen.getByTestId('detail')));
      expect(scrollIntoView).toHaveBeenCalledTimes(1);

      view.rerender(<NavigationProbe selection="" ready={false} />);
      const other = screen.getByRole('textbox', { name: 'Other focus target' });
      other.focus();
      view.rerender(<NavigationProbe selection="" ready />);
      await waitFor(() => expect(document.activeElement).toBe(other));
      view.rerender(<NavigationProbe selection="" ready={false} />);
      view.rerender(<NavigationProbe selection="" ready />);
      expect(document.activeElement).toBe(other);
      expect(scrollIntoView).toHaveBeenCalledTimes(1);
    } finally {
      HTMLElement.prototype.scrollIntoView = originalScrollIntoView;
    }
  });
});
