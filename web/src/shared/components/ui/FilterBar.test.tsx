import { screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { FilterBar } from './FilterBar';

it('allows invalid input to be reset while preserving explicit in-flight protection', async () => {
  const reset = vi.fn();
  const tree = (busy: boolean) => (
    <FilterBar
      activeCount={1}
      search={<input aria-label="Query" />}
      onSubmit={vi.fn()}
      onReset={reset}
      submitDisabled
      resetDisabled={busy}
    />
  );
  const view = await renderWithProviders(tree(false), { station: 'user', role: 'user' });
  expect(screen.getByRole('button', { name: 'Search' })).toBeDisabled();
  await view.user.click(screen.getByRole('button', { name: 'Reset' }));
  expect(reset).toHaveBeenCalledTimes(1);
  view.rerender(tree(true));
  expect(screen.getByRole('button', { name: 'Reset' })).toBeDisabled();
});
