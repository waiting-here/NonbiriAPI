import { useState } from 'react';
import { screen, waitFor } from '@testing-library/react';
import { describe, expect, test, vi } from 'vitest';
import { ConfirmDialog } from '../../src/shared/components/ConfirmDialog';
import { renderWithProviders } from './support';

function DialogHarness() {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>Open dialog</button>
      <button type="button" onClick={() => setBusy(true)}>Start work</button>
      <button type="button" onClick={() => setBusy(false)}>Stop work</button>
      <ConfirmDialog
        open={open}
        title="Delete record"
        description="This cannot be undone."
        confirmLabel="Delete"
        busy={busy}
        onConfirm={vi.fn()}
        onCancel={() => setOpen(false)}
      />
    </>
  );
}

describe('ConfirmDialog modal focus lifecycle', () => {
  test('keeps focus inside a busy dialog and restores the trigger only on close', async () => {
    const rendered = await renderWithProviders(<DialogHarness />, { station: 'user' });
    const trigger = screen.getByRole('button', { name: 'Open dialog' });
    await rendered.user.click(trigger);
    expect(screen.getByRole('button', { name: 'Cancel' })).toHaveFocus();

    await rendered.user.click(screen.getByRole('button', { name: 'Start work' }));
    const dialog = screen.getByRole('alertdialog');
    await waitFor(() => expect(dialog).toHaveFocus());
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled();
    await rendered.user.keyboard('{Escape}');
    expect(screen.getByRole('alertdialog')).toBeInTheDocument();

    await rendered.user.click(screen.getByRole('button', { name: 'Stop work' }));
    await rendered.user.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();

    // Busy-to-idle does not tear down the modal lifecycle; only the explicit
    // close above restores the trigger.
    expect(dialog).toHaveAttribute('tabindex', '-1');
  });

  test('focuses the dialog container when every action is disabled', async () => {
    const rendered = await renderWithProviders(
      <ConfirmDialog
        open
        title="Working"
        description="Please wait."
        confirmLabel="Confirm"
        busy
        onConfirm={vi.fn()}
        onCancel={vi.fn()}
      />,
      { station: 'user' },
    );
    const dialog = screen.getByRole('alertdialog');
    await waitFor(() => expect(dialog).toHaveFocus());
    expect(dialog).toHaveAttribute('tabindex', '-1');
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Working…' })).toBeDisabled();
    rendered.unmount();
  });
});
