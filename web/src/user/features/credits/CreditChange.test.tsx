import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { CreditChange } from './CreditChange';

describe('credit changes', () => {
  it('keeps exact grouped amounts, explicit signs, and a neutral zero', () => {
    render(
      <>
        <CreditChange value="9007199254740993.123" />
        <CreditChange value="-2000.007" />
        <CreditChange value="0" />
      </>,
    );
    expect(screen.getByText('9,007,199,254,740,993.123').parentElement).toHaveClass('is-income');
    expect(screen.getByText('2,000.007').parentElement).toHaveClass('is-expense');
    expect(screen.getByText('9,007,199,254,740,993.123').parentElement).toHaveTextContent(
      '+9,007,199,254,740,993.123',
    );
    expect(screen.getByText('2,000.007').parentElement).toHaveTextContent('−2,000.007');
    const zero = screen.getByText('0');
    expect(zero.parentElement).toHaveTextContent(/^0$/);
    expect(zero.parentElement).not.toHaveClass('is-income', 'is-expense');
  });
});
