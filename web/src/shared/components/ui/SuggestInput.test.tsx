import { useState } from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { Field } from './Field';
import { SuggestInput } from './SuggestInput';

function Harness() {
  const [value, setValue] = useState('');
  return (
    <>
      <Field
        label="Model"
        help="Any model name"
        error={value === 'bad' ? 'Unavailable' : undefined}
      >
        {(props) => (
          <SuggestInput
            {...props}
            value={value}
            onChange={setValue}
            suggestions={['alpha', 'beta']}
          />
        )}
      </Field>
      <button>Next</button>
      <output>{value}</output>
    </>
  );
}
describe('freeform suggestions', () => {
  it.each(['disabled', 'readOnly'] as const)(
    'preserves an edited prior selection through %s, reopening, Escape and blur',
    async (lock) => {
      function Editable({ locked = false }: { locked?: boolean }) {
        const [value, setValue] = useState('');
        return (
          <>
            <SuggestInput
              aria-label="Model"
              value={value}
              onChange={setValue}
              suggestions={['alpha', 'beta']}
              disabled={locked && lock === 'disabled'}
              readOnly={locked && lock === 'readOnly'}
            />
            <button>Next</button>
          </>
        );
      }
      const user = userEvent.setup();
      const view = render(<Editable />);
      const input = screen.getByRole('combobox', { name: 'Model' });
      await user.click(input);
      await user.keyboard('{ArrowDown}{Enter}');
      expect(input).toHaveValue('alpha');
      await user.clear(input);
      await user.type(input, 'a');
      await user.keyboard('{ArrowDown}');
      expect(screen.getByRole('listbox')).toBeVisible();
      view.rerender(<Editable locked />);
      expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
      expect(input).toHaveValue('a');
      await user.keyboard('{Enter}');
      expect(input).toHaveValue('a');
      view.rerender(<Editable />);
      await user.click(input);
      await user.keyboard('{Escape}{Tab}');
      expect(input).toHaveValue('a');
      await user.clear(input);
      await user.keyboard('{Escape}{Tab}');
      expect(input).toHaveValue('');
      await user.type(input, 'b');
      await user.keyboard('{ArrowDown}{Enter}');
      expect(input).toHaveValue('beta');
    },
  );

  it('closes an already open list and preserves the draft when its form becomes busy', async () => {
    const user = userEvent.setup();
    const changed = vi.fn();
    const view = render(
      <SuggestInput aria-label="Model" value="a" suggestions={['alpha']} onChange={changed} />,
    );
    await user.click(screen.getByRole('combobox'));
    await user.keyboard('{ArrowDown}');
    expect(screen.getByRole('option', { name: 'alpha' })).toBeVisible();
    view.rerender(
      <SuggestInput
        disabled
        aria-label="Model"
        value="a"
        suggestions={['alpha']}
        onChange={changed}
      />,
    );
    expect(screen.queryByRole('option')).not.toBeInTheDocument();
    expect(screen.getByRole('combobox')).toBeDisabled();
    expect(screen.getByRole('combobox')).toHaveValue('a');
    await user.keyboard('{Enter}');
    expect(changed).not.toHaveBeenCalled();
  });

  it('keeps labels, keyboard selection, custom values and clear input', async () => {
    const user = userEvent.setup();
    render(<Harness />);
    const input = screen.getByRole('combobox', { name: 'Model' });
    expect(input).toHaveAccessibleDescription('Any model name');
    await user.click(input);
    await user.keyboard('{ArrowDown}{Enter}');
    await waitFor(() => expect(input).toHaveValue('alpha'));
    await user.clear(input);
    await user.type(input, 'custom-model');
    await user.tab();
    expect(input).toHaveValue('custom-model');
    await user.clear(input);
    await user.tab();
    expect(input).toHaveValue('');
  });
  it('links dynamic error text to the actual input and closes suggestions before leaving', async () => {
    const user = userEvent.setup();
    render(<Harness />);
    const input = screen.getByRole('combobox', { name: 'Model' });
    await user.type(input, 'bad');
    expect(input).toHaveAttribute('aria-invalid', 'true');
    expect(input).toHaveAccessibleDescription('Unavailable Any model name');
    await user.keyboard('{Escape}{Tab}');
    expect(screen.getByRole('button', { name: 'Next' })).toHaveFocus();
  });
});
