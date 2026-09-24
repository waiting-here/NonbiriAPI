import { screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { loanReceipt } from '../../../test/fixtures/loans';
import { LoanFacts } from './LoanDetails';

describe('loan fact projections', () => {
  it('shows the five owner amounts in order and retains management evidence separately', async () => {
    const loan = loanReceipt();
    const view = await renderWithProviders(<LoanFacts loan={loan} projection="owner" />, {
      station: 'user',
    });
    const fields = Array.from(view.container.querySelectorAll('.loan-facts > div'));
    expect(fields.map((field) => field.querySelector('dt')?.textContent)).toEqual([
      'Principal',
      'Fee',
      'Game credits received',
      'Interest',
      'General credits deducted',
    ]);
    expect(fields.map((field) => field.querySelector('dd')?.textContent)).toEqual([
      loan.nominal,
      loan.fee,
      loan.disbursed,
      loan.interest,
      loan.repayment,
    ]);
    expect(view.container).not.toHaveTextContent(/coefficient|before|after|\(|\)/i);
    view.rerender(<LoanFacts loan={loan} projection="management" />);
    expect(screen.getByText('Disbursement coefficient A')).toBeVisible();
    expect(screen.getByText('Repayment coefficient B')).toBeVisible();
    const balances = screen.getByText('General credits: before → after').closest('div')!;
    expect(
      within(balances).getByText(`${loan.general_before} → ${loan.general_after}`),
    ).toBeVisible();
  });
});
