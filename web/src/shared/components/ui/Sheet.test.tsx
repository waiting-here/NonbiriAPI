import { useState } from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { ConfirmDialog } from '../ConfirmDialog';
import { Fold } from './Fold';
import { Sheet } from './Sheet';

function Harness({
  onClose = () => undefined,
  keepMounted = false,
}: {
  onClose?: () => void;
  keepMounted?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [confirming, setConfirming] = useState(false);
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        Add service
      </button>
      <button type="button">Background</button>
      <Sheet
        open={open}
        keepMounted={keepMounted}
        title="Add service"
        closeLabel="Close"
        onClose={() => {
          onClose();
          setOpen(false);
        }}
      >
        <label>
          Address
          <input />
        </label>
        <button type="button" onClick={() => setConfirming(true)}>
          Remove key
        </button>
      </Sheet>
      <ConfirmDialog
        open={confirming}
        title="Remove this key?"
        description="The key stops working."
        confirmLabel="Remove"
        onConfirm={() => setConfirming(false)}
        onCancel={() => setConfirming(false)}
      />
    </>
  );
}

describe('Sheet', () => {
  it('moves focus into the sheet, keeps Tab inside and returns focus on close', async () => {
    const user = userEvent.setup();
    render(<Harness />);
    const trigger = screen.getByRole('button', { name: 'Add service' });
    await user.click(trigger);
    const sheet = screen.getByRole('dialog', { name: 'Add service' });
    expect(sheet).toHaveAttribute('aria-modal', 'true');
    expect(sheet.contains(document.activeElement)).toBe(true);
    for (let step = 0; step < 6; step++) {
      await user.tab();
      expect(sheet.contains(document.activeElement)).toBe(true);
    }
    await user.keyboard('{Shift>}{Tab}{/Shift}');
    expect(sheet.contains(document.activeElement)).toBe(true);
    await user.click(screen.getByRole('button', { name: 'Close' }));
    expect(screen.queryByRole('dialog', { name: 'Add service' })).not.toBeInTheDocument();
    await waitFor(() => expect(trigger).toHaveFocus());
  });

  it('closes with Escape unless a dialog opened from the sheet handles it', async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    render(<Harness onClose={onClose} />);
    await user.click(screen.getByRole('button', { name: 'Add service' }));
    await user.click(screen.getByRole('button', { name: 'Remove key' }));
    expect(screen.getByRole('alertdialog', { name: 'Remove this key?' })).toBeVisible();
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
    expect(screen.getByRole('dialog', { name: 'Add service' })).toBeVisible();
    await user.keyboard('{Escape}');
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('keeps the sheet open while busy', async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    render(
      <Sheet open busy title="Saving" closeLabel="Close" onClose={onClose}>
        <p>Working</p>
      </Sheet>,
    );
    expect(screen.getByRole('button', { name: 'Close' })).toBeDisabled();
    await user.keyboard('{Escape}');
    expect(onClose).not.toHaveBeenCalled();
  });

  it('wraps around visible controls after a visited disclosure is collapsed', async () => {
    const user = userEvent.setup();
    render(
      <Sheet open title="Setup" closeLabel="Close" onClose={vi.fn()}>
        <button type="button">Visible action</button>
        <Fold title="Advanced" lazy>
          <button type="button">Collapsed action</button>
        </Fold>
        <fieldset disabled>
          <button type="button">Disabled by fieldset</button>
        </fieldset>
        <div style={{ display: 'none' }}>
          <button type="button">Hidden by CSS</button>
        </div>
        <div style={{ visibility: 'hidden' }}>
          <button type="button">Invisible action</button>
        </div>
        <div inert>
          <button type="button">Inert action</button>
        </div>
      </Sheet>,
    );
    const summary = screen.getByText('Advanced').closest('summary')!;
    await user.click(summary);
    await user.click(summary);
    screen.getByRole('heading', { name: 'Setup' }).focus();
    await user.tab({ shift: true });
    expect(summary).toHaveFocus();
    await user.tab();
    expect(screen.getByRole('button', { name: 'Close' })).toHaveFocus();
  });

  it('mounts a retained task on first open and preserves it while dismissed', async () => {
    const user = userEvent.setup();
    render(<Harness keepMounted />);
    expect(screen.queryByLabelText('Address')).not.toBeInTheDocument();
    const trigger = screen.getByRole('button', { name: 'Add service' });
    await user.click(trigger);
    await user.type(screen.getByLabelText('Address'), 'https://draft.example/v1');
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(document.body.style.overflow).not.toBe('hidden');
    expect(trigger).toHaveFocus();
    await user.click(trigger);
    expect(screen.getByLabelText('Address')).toHaveValue('https://draft.example/v1');
  });
});
