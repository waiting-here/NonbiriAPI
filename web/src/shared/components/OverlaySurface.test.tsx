import { useState } from 'react';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { NativeDialog } from './NativeDialog';
import { ConfirmDialog } from './ConfirmDialog';
import { MoreMenu } from './ui/MoreMenu';
import { Sheet } from './ui/Sheet';

function Nested() {
  const [confirm, setConfirm] = useState(false);
  return (
    <>
      <button>Background</button>
      <NativeDialog aria-label="Game" onDismiss={() => undefined}>
        <MoreMenu label="Actions" items={[{ label: 'Remove', onSelect: () => setConfirm(true) }]} />
        <ConfirmDialog
          open={confirm}
          title="Remove item"
          description="Review"
          confirmLabel="Remove"
          onConfirm={() => setConfirm(false)}
          onCancel={() => setConfirm(false)}
        />
      </NativeDialog>
    </>
  );
}
describe('overlay integration', () => {
  it('consumes Escape in a busy confirmation before its native parent receives it', async () => {
    const user = userEvent.setup();
    const nativeKeyDown = vi.fn(),
      closeNative = vi.fn(),
      closeConfirm = vi.fn();
    render(
      <NativeDialog aria-label="Native parent" onDismiss={closeNative} onKeyDown={nativeKeyDown}>
        <ConfirmDialog
          open
          busy
          title="Saving"
          description="Please wait"
          confirmLabel="Save"
          onConfirm={() => undefined}
          onCancel={closeConfirm}
        />
      </NativeDialog>,
    );
    const confirm = screen.getByRole('alertdialog', { name: 'Saving' });
    await waitFor(() => expect(confirm).toHaveFocus());
    await user.keyboard('{Escape}');
    expect(nativeKeyDown).not.toHaveBeenCalled();
    expect(closeNative).not.toHaveBeenCalled();
    expect(closeConfirm).not.toHaveBeenCalled();
    expect(confirm).toBeVisible();
    expect(screen.getByRole('dialog', { name: 'Native parent' })).toHaveProperty('open', true);
  });

  it.each([false, true])(
    'leaves Escape to native cancel inside a Sheet (busy %s)',
    async (busy) => {
      const user = userEvent.setup();
      const closeSheet = vi.fn(),
        closeNative = vi.fn(),
        keyDown = vi.fn();
      render(
        <Sheet open title="Outer sheet" closeLabel="Close sheet" onClose={closeSheet}>
          <NativeDialog
            aria-label="Inner native"
            onDismiss={closeNative}
            busy={busy}
            onKeyDown={keyDown}
          >
            <button>Native action</button>
          </NativeDialog>
        </Sheet>,
      );
      const native = screen.getByRole('dialog', { name: 'Inner native' });
      await waitFor(() =>
        expect(screen.getByRole('button', { name: 'Native action' })).toHaveFocus(),
      );
      await user.keyboard('{Escape}');
      expect(keyDown).toHaveBeenCalled();
      expect(keyDown.mock.calls.at(-1)?.[0].defaultPrevented).toBe(false);
      expect(closeSheet).not.toHaveBeenCalled();
      expect(native).toHaveProperty('open', true);
      // jsdom has no native cancel dispatch; the browser composition test presses real Escape.
      fireEvent(native, new Event('cancel', { cancelable: true }));
      expect(closeNative).toHaveBeenCalledTimes(busy ? 0 : 1);
      expect(screen.getByRole('dialog', { name: 'Outer sheet' })).toBeVisible();
    },
  );

  it('keeps menu and confirmation portals in a native top-layer dialog', async () => {
    const user = userEvent.setup();
    render(<Nested />);
    const native = screen.getByRole('dialog', { name: 'Game' });
    await user.click(screen.getByRole('button', { name: 'Actions' }));
    const menu = screen.getByRole('menu');
    expect(native).toContainElement(menu);
    const ids = [...menu.querySelectorAll('[id]')].map((node) => node.id);
    expect(new Set(ids).size).toBe(ids.length);
    await user.click(screen.getByRole('menuitem', { name: 'Remove' }));
    const confirm = screen.getByRole('alertdialog', { name: 'Remove item' });
    expect(native).toContainElement(confirm);
    await waitFor(() => expect(confirm.contains(document.activeElement)).toBe(true));
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
    expect(native).toBeVisible();
  });
  it('moves the existing portal on fullscreen changes without resetting a draft', async () => {
    const user = userEvent.setup();
    render(
      <>
        <div data-testid="scene" />
        <Sheet open title="Draft" closeLabel="Close" onClose={() => undefined}>
          <label>
            Address
            <input defaultValue="saved" />
          </label>
        </Sheet>
      </>,
    );
    const input = screen.getByLabelText('Address');
    await user.type(input, '-editing');
    const scene = screen.getByTestId('scene');
    const descriptor = Object.getOwnPropertyDescriptor(document, 'fullscreenElement');
    try {
      Object.defineProperty(document, 'fullscreenElement', { configurable: true, value: scene });
      act(() => document.dispatchEvent(new Event('fullscreenchange')));
      expect(scene).toContainElement(input);
      expect(input).toHaveValue('saved-editing');
      Object.defineProperty(document, 'fullscreenElement', { configurable: true, value: null });
      act(() => document.dispatchEvent(new Event('fullscreenchange')));
      expect(scene).not.toContainElement(input);
      expect(screen.getByLabelText('Address')).toBe(input);
    } finally {
      if (descriptor) Object.defineProperty(document, 'fullscreenElement', descriptor);
      else Reflect.deleteProperty(document, 'fullscreenElement');
    }
  });
});
