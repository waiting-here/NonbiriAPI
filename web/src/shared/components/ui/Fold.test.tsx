import { fireEvent, render, screen } from '@testing-library/react';
import { useEffect, useState } from 'react';
import { expect, it, vi } from 'vitest';
import { Fold } from './Fold';

it('mounts expensive content on first expansion and preserves it after closing', () => {
  const load = vi.fn();
  function Content() {
    useEffect(load, []);
    return <input aria-label="Draft" defaultValue="original" />;
  }
  render(
    <Fold title="History" lazy>
      <Content />
    </Fold>,
  );
  expect(load).not.toHaveBeenCalled();
  fireEvent.click(screen.getByText('History'));
  expect(load).toHaveBeenCalledOnce();
  fireEvent.change(screen.getByLabelText('Draft'), { target: { value: 'edited' } });
  fireEvent.click(screen.getByText('History'));
  fireEvent.click(screen.getByText('History'));
  expect(screen.getByLabelText('Draft')).toHaveValue('edited');
  expect(load).toHaveBeenCalledOnce();
});

it('supports a controlled disclosure without losing native keyboard semantics', () => {
  function Controlled() {
    const [open, setOpen] = useState(false);
    return (
      <Fold title="Settings" open={open} onOpenChange={setOpen}>
        <p>Ready</p>
      </Fold>
    );
  }
  const view = render(<Controlled />);
  expect(view.container.querySelector('details')).not.toHaveAttribute('open');
  fireEvent.click(screen.getByText('Settings'));
  expect(view.container.querySelector('details')).toHaveAttribute('open');
  fireEvent.click(screen.getByText('Settings'));
  expect(view.container.querySelector('details')).not.toHaveAttribute('open');
});
