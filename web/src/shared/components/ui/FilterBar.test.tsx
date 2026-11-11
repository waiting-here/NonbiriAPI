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

it.each(['form', 'div'] as const)(
  'prevents duplicate Enter submissions in a disabled %s search',
  async (as) => {
    const submit = vi.fn();
    const view = await renderWithProviders(
      <FilterBar
        as={as}
        activeCount={0}
        search={<input aria-label="Query" />}
        onSubmit={submit}
        onReset={vi.fn()}
        submitDisabled
      />,
      { station: 'user' },
    );
    await view.user.type(screen.getByRole('textbox', { name: 'Query' }), 'pending{Enter}');
    expect(submit).not.toHaveBeenCalled();
    expect(screen.getByRole('textbox', { name: 'Query' })).toHaveValue('pending');
  },
);
