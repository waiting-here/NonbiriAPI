import { useState } from 'react';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
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
