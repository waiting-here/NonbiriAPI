import { fireEvent, render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { GachaDialog } from './GachaDialog';

it('Escape closes only the top dialog and does not change another scroll lock', () => {
  const outer = vi.fn(),
    inner = vi.fn();
  document.body.style.overflow = 'auto';
  const view = render(
    <GachaDialog title="Results" onClose={outer}>
      <GachaDialog title="Artwork" onClose={inner}>
        Portrait
      </GachaDialog>
    </GachaDialog>,
  );
  fireEvent(
    screen.getByRole('dialog', { name: 'Artwork' }),
    new Event('cancel', { cancelable: true }),
  );
  expect(inner).toHaveBeenCalledOnce();
  expect(outer).not.toHaveBeenCalled();
  view.unmount();
  expect(document.body.style.overflow).toBe('auto');
  document.body.style.overflow = '';
});
