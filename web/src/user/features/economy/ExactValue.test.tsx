import { render, screen } from '@testing-library/react';
import { expect, it } from 'vitest';
import { CreditAmount } from './ExactValue';

it.each([
  ['-0.001', '-0.001'],
  ['-9007199254740993.999', '-9,007,199,254,740,993.999'],
  ['0', '0'],
])('displays the signed credit value %s without applying price rules', (value, displayed) => {
  render(<CreditAmount value={value} unit="credits" />);
  expect(screen.getByText(displayed)).toBeVisible();
  expect(screen.getByText('credits')).toBeVisible();
});
