import { useState } from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, it } from 'vitest';
import { ExpandablePanel } from './ExpandablePanel';

it('keeps an inline draft open after outside clicks and Escape, then restores trigger focus', async () => {
  function Form() {
    const [open, setOpen] = useState(false);
    const [busy, setBusy] = useState(false);
    return (
      <>
        <button onClick={() => setOpen(true)}>Settings</button>
        <button>Elsewhere</button>
        <ExpandablePanel
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
        </ExpandablePanel>
      </>
    );
  }
  const user = userEvent.setup();
  render(<Form />);
  const trigger = screen.getByRole('button', { name: 'Settings' });
  await user.click(trigger);
  expect(screen.getByRole('heading', { name: 'Game settings' })).toHaveFocus();
  await user.clear(screen.getByRole('textbox', { name: 'Price' }));
  await user.type(screen.getByRole('textbox', { name: 'Price' }), '25');
  await user.click(screen.getByRole('button', { name: 'Elsewhere' }));
  await user.keyboard('{Escape}');
  expect(screen.getByRole('region', { name: 'Game settings' })).toBeVisible();
  expect(document.body.style.overflow).not.toBe('hidden');
  await user.click(screen.getByRole('button', { name: 'Save' }));
  expect(screen.getByRole('button', { name: 'Close' })).toBeDisabled();
  await user.click(screen.getByRole('button', { name: 'Finish' }));
  await user.click(screen.getByRole('button', { name: 'Close' }));
  expect(screen.queryByRole('region', { name: 'Game settings' })).not.toBeInTheDocument();
  expect(trigger).toHaveFocus();
  await user.click(trigger);
  expect(screen.getByRole('textbox', { name: 'Price' })).toHaveValue('25');
});
