import { useState } from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterAll, beforeAll, expect, it } from 'vitest';
import { Drawer } from './Drawer';

const modal = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, 'showModal');
const close = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, 'close');
beforeAll(() => {
  Object.defineProperty(HTMLDialogElement.prototype, 'showModal', {
    configurable: true,
    value() {
      this.setAttribute('open', '');
    },
  });
  Object.defineProperty(HTMLDialogElement.prototype, 'close', {
    configurable: true,
    value() {
      this.removeAttribute('open');
    },
  });
});
afterAll(() => {
  if (modal) Object.defineProperty(HTMLDialogElement.prototype, 'showModal', modal);
  else Reflect.deleteProperty(HTMLDialogElement.prototype, 'showModal');
  if (close) Object.defineProperty(HTMLDialogElement.prototype, 'close', close);
  else Reflect.deleteProperty(HTMLDialogElement.prototype, 'close');
});

it('preserves a draft across close and keeps a busy form open on cancellation', async () => {
  function Form() {
    const [open, setOpen] = useState(false);
    const [busy, setBusy] = useState(false);
    return (
      <>
        <button onClick={() => setOpen(true)}>Settings</button>
        <Drawer
          open={open}
          onClose={() => setOpen(false)}
          title="Game settings"
          closeLabel="Close"
          busy={busy}
        >
          <label>
            Price
            <input defaultValue="10" />
          </label>
          <button onClick={() => setBusy(!busy)}>{busy ? 'Finish' : 'Save'}</button>
        </Drawer>
      </>
    );
  }
  const user = userEvent.setup();
  render(<Form />);
  const trigger = screen.getByRole('button', { name: 'Settings' });
  await user.click(trigger);
  await user.clear(screen.getByRole('textbox', { name: 'Price' }));
  await user.type(screen.getByRole('textbox', { name: 'Price' }), '25');
  await user.click(screen.getByRole('button', { name: 'Close' }));
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(trigger).toHaveFocus();
  await user.click(trigger);
  expect(screen.getByRole('textbox', { name: 'Price' })).toHaveValue('25');
  await user.click(screen.getByRole('button', { name: 'Save' }));
  fireEvent(screen.getByRole('dialog'), new Event('cancel', { cancelable: true }));
  expect(screen.getByRole('dialog')).toBeVisible();
  expect(screen.getByRole('button', { name: 'Close' })).toBeDisabled();
  await user.click(screen.getByRole('button', { name: 'Finish' }));
  fireEvent(screen.getByRole('dialog'), new Event('cancel', { cancelable: true }));
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(trigger).toHaveFocus();
});
