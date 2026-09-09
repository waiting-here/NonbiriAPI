import { act, fireEvent, screen } from '@testing-library/react';
import { useState } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { creditsFromMilli } from '../common/strict';
import { ActionControls } from './ActionControls';
import { normalizeRPSState } from './normalize';
import { rpsStateWire } from './testFixtures';
import type { RPSState } from './types';

function dealer(balances = ['40', '29.001', '35'], base = '5') {
  const wire = rpsStateWire('dealer_raise');
  wire.rule_snapshot.base = base;
  wire.seats.forEach((seat, index) => {
    seat.current_balance = balances[index];
    seat.starting_balance = balances[index];
    seat.total_returned = '1';
  });
  return normalizeRPSState(wire);
}

describe('RPS authoritative raise controls', () => {
  it('adjusts by the frozen base, clamps empty and zero drafts, and never submits a shortcut', async () => {
    const onAction = vi.fn(),
      onSelection = vi.fn();
    const { user } = await renderWithProviders(
      <ActionControls
        state={dealer()}
        busy={false}
        current
        onAction={onAction}
        onSelection={onSelection}
      />,
      { station: 'user' },
    );
    const input = screen.getByRole('textbox', { name: 'Dealer raise' });
    fireEvent.change(input, { target: { value: '' } });
    await user.click(screen.getByRole('button', { name: '+5 credits' }));
    expect(input).toHaveValue('5');
    await user.click(screen.getByRole('button', { name: '−5 credits' }));
    expect(input).toHaveValue('0');
    expect(screen.getByRole('button', { name: 'Raise' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Do not raise' })).toBeEnabled();
    await user.click(screen.getByRole('button', { name: 'Max 29 credits' }));
    await user.click(screen.getByRole('button', { name: '+5 credits' }));
    expect(input).toHaveValue('29');
    expect(onAction).not.toHaveBeenCalled();
    expect(onSelection).toHaveBeenCalledTimes(4);
    await user.click(screen.getByRole('button', { name: 'Raise' }));
    expect(onAction).toHaveBeenCalledExactlyOnceWith({
      action: 'dealer_decision',
      payload: { decision: 'raise', amount: '29' },
    });
  });

  it.each([
    [['20', '29.001', '35'], 'All in 20 credits'],
    [['20.001', '29.001', '35'], 'Max 20 credits'],
    [['40', '29.001', '35'], 'Max 29 credits'],
  ])('uses all-in only for the entire legal own balance %j', async (balances, label) => {
    await renderWithProviders(
      <ActionControls
        state={dealer(balances as string[])}
        busy={false}
        current
        onAction={vi.fn()}
      />,
      { station: 'user' },
    );
    expect(screen.getByRole('button', { name: label as string })).toBeEnabled();
  });

  it('preserves a fractional base step without rounding it into a different stake', async () => {
    const { user } = await renderWithProviders(
      <ActionControls state={dealer(undefined, '1.5')} busy={false} current onAction={vi.fn()} />,
      { station: 'user' },
    );
    const input = screen.getByRole('textbox', { name: 'Dealer raise' });
    fireEvent.change(input, { target: { value: '' } });
    await user.click(screen.getByRole('button', { name: '+1.5 credits' }));
    expect(input).toHaveValue('1.5');
    expect(screen.getByRole('button', { name: 'Raise' })).toBeDisabled();
    await user.click(screen.getByRole('button', { name: '+1.5 credits' }));
    expect(input).toHaveValue('3');
    expect(screen.getByRole('button', { name: 'Raise' })).toBeEnabled();
  });

  it('rechecks a changing authoritative cap and keeps wide balances exact', async () => {
    const full = (1n << 128n) - 1n,
      maximum = (full / 1000n).toString();
    let update!: (state: RPSState) => void;
    function Harness() {
      const [state, setState] = useState(dealer(Array(3).fill(creditsFromMilli(full))));
      update = setState;
      return <ActionControls state={state} busy={false} current onAction={vi.fn()} />;
    }
    const { user } = await renderWithProviders(<Harness />, { station: 'user' });
    await user.click(screen.getByRole('button', { name: /^Max / }));
    expect(screen.getByRole('textbox')).toHaveValue(maximum);
    expect(screen.getByRole('button', { name: 'Raise' })).toBeEnabled();
    act(() => update(dealer(['2.999', '3', '4'])));
    expect(screen.getByRole('button', { name: 'Raise' })).toBeDisabled();
    await user.click(screen.getByRole('button', { name: 'Max 2 credits' }));
    expect(screen.getByRole('textbox')).toHaveValue('2');
  });

  it('shows only no-raise below one whole credit and keeps disabled controls silent', async () => {
    const onAction = vi.fn(),
      onSelection = vi.fn();
    const { user } = await renderWithProviders(
      <ActionControls
        state={dealer(['0.999', '5', '8'])}
        busy
        current
        onAction={onAction}
        onSelection={onSelection}
      />,
      { station: 'user' },
    );
    expect(screen.getAllByRole('button')).toHaveLength(1);
    await user.click(screen.getByRole('button', { name: 'Do not raise' }));
    expect(onAction).not.toHaveBeenCalled();
    expect(onSelection).not.toHaveBeenCalled();
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
  });

  it('displays the authoritative call amount beside the action and waits on disconnect or missing amount', async () => {
    const source = normalizeRPSState(rpsStateWire('followers'));
    let update!: (next: { state: RPSState; current: boolean }) => void;
    function Harness() {
      const [value, setValue] = useState({ state: source, current: true });
      update = setValue;
      return <ActionControls {...value} busy={false} onAction={vi.fn()} />;
    }
    await renderWithProviders(<Harness />, { station: 'user' });
    expect(screen.getByText('Required to call: 1 credits')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Call' })).toBeEnabled();
    act(() => update({ state: source, current: false }));
    expect(screen.getByText('Waiting for the current decision to sync…')).toBeInTheDocument();
    expect(screen.queryByText('Required to call: 1 credits')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Call' })).toBeDisabled();
    act(() =>
      update({
        state: { ...source, economy: { ...source.economy, dealerRaise: null } },
        current: true,
      }),
    );
    expect(screen.getByRole('button', { name: 'Call' })).toBeDisabled();
  });
});
