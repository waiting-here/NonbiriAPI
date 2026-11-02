import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { CreditChange } from './CreditChange';

describe('credit changes', () => {
  it('keeps exact grouped amounts, explicit signs, and a neutral zero', () => {
    const { container } = render(
      <>
        <CreditChange value="9007199254740993.123" />
        <CreditChange value="-2000.007" />
        <CreditChange value="0" />
      </>,
    );
    const changes = container.querySelectorAll('.credit-change');
    expect(changes).toHaveLength(3);
    expect(changes[0]).toHaveClass('is-income');
    expect(changes[0]).toHaveTextContent(/^\+9,007,199,254,740,993\.123$/);
    expect(changes[1]).toHaveClass('is-expense');
    expect(changes[1]).toHaveTextContent(/^−2,000\.007$/);
    expect(changes[2]).toHaveTextContent(/^0$/);
    expect(changes[2]).not.toHaveClass('is-income', 'is-expense');
  });
});
