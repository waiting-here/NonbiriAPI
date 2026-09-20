import { act, fireEvent, screen, within } from '@testing-library/react';
import { QueryClient } from '@tanstack/react-query';
import { describe, expect, it } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { clearManagementSession } from '@shared/charityManagement';
import { ToastProvider } from '@shared/components/Toast';
import { OnboardingCard } from './OnboardingCard';
import { gameKeys, normalizeGamesSnapshot } from './snapshot';
import { gamesSnapshotWire } from './testFixtures';
import { spendableGameCredits } from './spendable';

describe('newcomer progress', () => {
  it('announces all four natural-win awards once while retaining the unearned bust task', async () => {
    const wire = gamesSnapshotWire();
    const card = () => <ToastProvider><OnboardingCard game="blackjack" progress={normalizeGamesSnapshot(wire).onboarding.blackjack} /></ToastProvider>;
    const rendered = await renderWithProviders(card(), { station: 'user' });
    for (const item of wire.onboarding.blackjack.items) item.completed = item.key !== 'first_bust';
    rendered.rerender(card());
    expect(screen.getAllByRole('status')).toHaveLength(4);
    expect(screen.getByRole('button', { name: /Newcomer rewards/ })).toHaveTextContent('1 tasks left · 3,000 general credits available');
    expect(within(screen.getByRole('list')).getAllByRole('listitem')).toHaveLength(1);
    rendered.rerender(card());
    expect(screen.getAllByRole('status')).toHaveLength(4);
  });

  it('starts expanded, retains remaining rewards when collapsed, and announces only newly earned rewards', async () => {
    const wire = gamesSnapshotWire();
    const card = () => <ToastProvider><OnboardingCard game="rps" progress={normalizeGamesSnapshot(wire).onboarding.rps} /></ToastProvider>;
    const rendered = await renderWithProviders(card(), { station: 'user' });
    const heading = screen.getByRole('button', { name: /Newcomer rewards/ });
    expect(heading).toHaveAttribute('aria-expanded', 'true');
    expect(heading).toHaveTextContent('3 tasks left · 8,000 general credits available');
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
    fireEvent.click(heading);
    expect(heading).toHaveAttribute('aria-expanded', 'false');
    expect(screen.queryByRole('list')).not.toBeInTheDocument();
    expect(heading).toHaveTextContent('8,000 general credits available');
    wire.onboarding.rps.items[0].completed = true;
    rendered.rerender(card());
    expect(screen.getByRole('status')).toHaveTextContent('Newcomer reward: +1,000 general credits');
    expect(heading).toHaveTextContent('2 tasks left · 7,000 general credits available');
    fireEvent.click(heading);
    expect(within(screen.getByRole('list')).getAllByRole('listitem')).toHaveLength(2);
    rendered.rerender(card());
    expect(screen.getAllByRole('status')).toHaveLength(1);
    for (const item of wire.onboarding.rps.items) item.completed = true;
    wire.onboarding.rps.all_completed = true;
    rendered.rerender(card());
    expect(screen.queryByRole('button', { name: /Newcomer rewards/ })).not.toBeInTheDocument();
    expect(screen.getAllByRole('status')).toHaveLength(3);
    rendered.rerender(card());
    expect(screen.getAllByRole('status')).toHaveLength(3);
  });

  it('keeps completed cards hidden on reload without announcing historical rewards', async () => {
    const wire = gamesSnapshotWire();
    for (const item of wire.onboarding.fishing.items) item.completed = true;
    wire.onboarding.fishing.all_completed = true;
    await renderWithProviders(
      <ToastProvider><OnboardingCard game="fishing" progress={normalizeGamesSnapshot(wire).onboarding.fishing} /></ToastProvider>,
      { station: 'user', locale: 'zh' },
    );
    expect(screen.queryByRole('button', { name: /新人奖励/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });

  it('evicts progress and both wallets at the existing account-session boundary', async () => {
    const client = new QueryClient();
    const old = normalizeGamesSnapshot(gamesSnapshotWire());
    client.setQueryData(gameKeys.snapshot, old);
    await act(async () => clearManagementSession(client, 'steward'));
    expect(client.getQueryData(gameKeys.snapshot)).toBeUndefined();
    const next = gamesSnapshotWire();
    next.balance = '0';
    next.game_balance = '50';
    client.setQueryData(gameKeys.snapshot, normalizeGamesSnapshot(next));
    expect(client.getQueryData(gameKeys.snapshot)).toMatchObject({ balance: '0', gameBalance: '50', onboarding: { rps: { allCompleted: false } } });
    client.clear();
  });
});
describe('game entry availability', () => {
  it('uses positive wallet balances independently and preserves milli-credit precision', () => {
    expect(spendableGameCredits({ balance: '-100', gameBalance: '1.001' })).toEqual({ general: '0', game: '1.001', total: '1.001' });
    expect(spendableGameCredits({ balance: '1.001', gameBalance: '-100' })).toEqual({ general: '1.001', game: '0', total: '1.001' });
    expect(spendableGameCredits({ balance: '1.001', gameBalance: '2.002' }).total).toBe('3.003');
    expect(spendableGameCredits({ balance: '-1', gameBalance: '-1' }).total).toBe('0');
  });
});
