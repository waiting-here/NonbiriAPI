import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useState } from 'react';
import { expect, it } from 'vitest';
import { PercentInput } from './PercentInput';

it('keeps the typed text while converting exact hundredths to basis points', async () => {
  function Editor() {
    const [value, setValue] = useState(0);
    return (
      <>
        <PercentInput
          aria-label="Fee"
          unit="%"
          max={99.99}
          basisPoints={value}
          onBasisChange={setValue}
        />
        <output>{Number.isNaN(value) ? 'invalid' : value}</output>
      </>
    );
  }
  const user = userEvent.setup();
  render(<Editor />);
  const input = screen.getByRole('textbox', { name: 'Fee' });
  for (const [raw, parsed] of [
    ['0.', '0'],
    ['0.05', '5'],
    ['1.05', '105'],
    ['1.50', '150'],
    ['99.99', '9999'],
    ['100', 'invalid'],
  ]) {
    await user.clear(input);
    await user.type(input, raw);
    expect(input).toHaveValue(raw);
    expect(screen.getByRole('status')).toHaveTextContent(parsed);
  }
  await user.clear(input);
  expect(input).toHaveValue('');
  expect(screen.getByRole('status')).toHaveTextContent('invalid');
});
