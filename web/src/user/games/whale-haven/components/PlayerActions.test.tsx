import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../../test/unit/support';
import { getQuote, type Profile, type Quote } from '../api';
import { ExchangePane } from './ExchangePane';
import { SocialPane, DepartureDialog } from './SocialPane';
import { WhaleNamingModal } from './WhaleNamingModal';
import type { NeighborStation } from '../game/types';

vi.mock('../api', async (original) => ({
  ...(await original<typeof import('../api')>()),
  getQuote: vi.fn(),
  searchNeighbors: vi.fn().mockResolvedValue([]),
}));

function profile() {
  return {
    snapshot: {
      revision: 8,
      wallet: {
        pearls: 20,
        conchShards: 5,
        dailyPearlExchanged: 0,
        dailySocialPearlsGained: 0,
        dailyConchShardsGained: 0,
        generalMilli: '99',
      },
    },
    settings_revision: '3',
    settings: {
      enabled: true,
      exchange_enabled: true,
      values: {
        lot_pearls: 5,
        lot_gross_milli: 1000000,
        guest_wage_bp: 2000,
        daily_social_pearls: 7,
        daily_conch_shards: 10,
        stove_window_seconds: 3600,
        stove_required_grams: 20,
        daily_exchange_pearls: 75,
        platform_cut_bp: 1000,
        welfare_cut_bp: 1000,
        thursday_cut_bp: 500,
      },
    },
    social: { door: 'ABC', guests: [], open: false, away: null },
  } as unknown as Profile;
}
const quote: Quote = {
  quantity: '0',
  max_lots: '4',
  settings_revision: '3',
  profile_revision: '8',
  source_pearls: '0',
  gross_general_milli: '0',
  platform_milli: '0',
  welfare_milli: '0',
  thursday_milli: '0',
  net_general_milli: '0',
  daily_source_remaining: '75',
  general_wallet_cap_milli: '300000000',
  wallet_cap_blocked: false,
};
const priced: Quote = {
  ...quote,
  quantity: '1',
  source_pearls: '5',
  gross_general_milli: '1000000',
  platform_milli: '100000',
  welfare_milli: '100000',
  thursday_milli: '50000',
  net_general_milli: '750000',
};
const props = () => ({
  busy: false,
  onExchange: vi.fn().mockResolvedValue(true),
  onCollect: vi.fn(),
  onRefresh: vi.fn(),
});
describe('player exchange limits and consent', () => {
  it.each([
    {
      locale: 'en',
      daily: '75',
      coins: 20,
      cap: true,
      reason: 'One lot would take your general balance above the cap of',
    },
    {
      locale: 'en',
      daily: '4',
      coins: 20,
      cap: false,
      reason: 'The daily allowance has less than one lot left.',
    },
    { locale: 'zh', daily: '75', coins: 4, cap: false, reason: '持有的小鲸币不足一整手。' },
  ] as const)(
    'explains zero lots on opening with $reason',
    async ({ locale, daily, coins, cap, reason }) => {
      const p = profile();
      p.snapshot.wallet.pearls = coins;
      vi.mocked(getQuote).mockResolvedValue({
        ...quote,
        max_lots: '0',
        daily_source_remaining: daily,
        wallet_cap_blocked: cap,
      });
      await renderWithProviders(<ExchangePane profile={p} {...props()} />, {
        station: 'user',
        locale,
      });
      expect(await screen.findByText(reason, { exact: false })).toBeInTheDocument();
      expect(document.querySelector('[aria-live="polite"]')).toHaveTextContent(
        locale === 'en' ? 'Available now 0 lots' : '最多可兑 0 手',
      );
      expect(document.querySelector('[aria-live="polite"]')).toHaveTextContent(daily);
      expect(getQuote).toHaveBeenCalledTimes(1);
      expect(vi.mocked(getQuote).mock.calls[0][0]).toBe('0');
      expect(
        screen.getByRole('button', { name: locale === 'en' ? 'Review quote' : '查看报价' }),
      ).toBeDisabled();
    },
  );
  it('shows posted balances separately from pending earnings and fractional wages', async () => {
    vi.mocked(getQuote).mockResolvedValue(quote);
    const p = profile(),
      act = props();
    Object.assign(p.snapshot.wallet, {
      pendingSocialPearls: 2,
      pendingConchShards: 3,
      wageProgress: 0.45,
    });
    const { user } = await renderWithProviders(<ExchangePane profile={p} {...act} />, {
      station: 'user',
      locale: 'en',
    });
    expect(screen.getByText('Pending settlement', { exact: false })).toHaveTextContent(
      'Whale coins 2 · Conch shards 3',
    );
    expect(screen.getByText(/Progress toward next wage coin/)).toHaveTextContent('45%');
    const balance = screen.getByText('Whale coin balance').parentElement!;
    expect(within(balance).getByRole('definition')).toHaveTextContent('20');
    await user.click(screen.getByRole('button', { name: 'Collect earnings' }));
    expect(act.onCollect).toHaveBeenCalledOnce();
  });
  it('prices positive lots, confirms the pool totals and reloads availability', async () => {
    const p = profile(),
      act = props();
    vi.mocked(getQuote).mockImplementation(async (n) => (n === '0' ? quote : priced));
    const { user } = await renderWithProviders(<ExchangePane profile={p} {...act} />, {
      station: 'user',
      locale: 'en',
    });
    await screen.findByText('Available now', { exact: false });
    await user.click(screen.getByRole('button', { name: 'Review quote' }));
    const dialog = within(await screen.findByRole('alertdialog'));
    expect(dialog.getByText('Platform').parentElement).toHaveTextContent('100');
    expect(dialog.getByText('Welfare pool').parentElement).toHaveTextContent('100');
    expect(dialog.getByText('Thursday pool').parentElement).toHaveTextContent('50');
    expect(dialog.getByText('Net credit').parentElement).toHaveTextContent('750');
    await user.click(dialog.getByRole('button', { name: 'Confirm exchange' }));
    await waitFor(() => expect(act.onRefresh).toHaveBeenCalledOnce());
    expect(act.onExchange).toHaveBeenCalledWith(priced, expect.any(Function));
    expect(vi.mocked(getQuote).mock.calls.map(([n]) => n)).toEqual(['0', '1', '0']);
  });
  it('disables a pending confirmation when a new profile or rule invalidates the quote', async () => {
    const p = profile(),
      act = props();
    vi.mocked(getQuote).mockImplementation(async (n) => (n === '0' ? quote : priced));
    const { rerender, user } = await renderWithProviders(<ExchangePane profile={p} {...act} />, {
      station: 'user',
      locale: 'en',
    });
    await screen.findByText('Available now', { exact: false });
    await user.click(screen.getByRole('button', { name: 'Review quote' }));
    await screen.findByRole('alertdialog');
    vi.mocked(getQuote).mockResolvedValue({
      ...quote,
      profile_revision: '9',
      max_lots: '0',
      wallet_cap_blocked: true,
    });
    const updated = {
      ...p,
      snapshot: {
        ...p.snapshot,
        revision: 9,
        wallet: { ...p.snapshot.wallet, generalMilli: '300000000' },
      },
    };
    rerender(<ExchangePane profile={updated} {...act} />);
    await screen.findByText('One lot would take', { exact: false });
    expect(
      within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Confirm exchange' }),
    ).toBeDisabled();
    expect(act.onExchange).not.toHaveBeenCalled();
  });
  it.each(['en', 'zh'] as const)('adopts with the keeper’s visit setting in %s', async (locale) => {
    const adopt = vi.fn();
    const { user } = await renderWithProviders(
      <WhaleNamingModal initialName="Fin" busy={false} onConfirmName={adopt} />,
      { station: 'user', locale },
    );
    await user.click(
      screen.getByRole('checkbox', {
        name: locale === 'en' ? 'Open my haven for mutual visits' : '开放小筑，允许互相串门',
      }),
    );
    await user.click(
      screen.getByRole('button', {
        name: locale === 'en' ? 'Adopt Fin' : '确认叫「Fin」，领养回家',
      }),
    );
    expect(adopt).toHaveBeenCalledWith('Fin', false);
  });
  it('requires separate visit hiring consent and discloses the wage allowance', async () => {
    const dispatch = vi.fn().mockResolvedValue(true);
    const peer = { id: 'next', gameProfilePublic: false, anonymousCode: '11' } as NeighborStation;
    const { user } = await renderWithProviders(
      <DepartureDialog busy={false} dispatch={dispatch} neighbor={peer} onClose={vi.fn()} />,
      { station: 'user', locale: 'en' },
    );
    expect(screen.getByRole('alertdialog')).toHaveAccessibleName('Visit Anonymous keeper #11?');
    const consent = screen.getByRole('checkbox', {
      name: 'Allow the host to hire my whale on this visit',
    });
    expect(consent).not.toBeChecked();
    await user.click(consent);
    expect(
      screen.getByText('Wages count toward your daily social whale coin limit.'),
    ).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Depart' }));
    expect(dispatch).toHaveBeenCalledWith(
      { type: 'send_visit', target: 'next', allowHire: true },
      expect.any(Function),
    );
  });
  it('does not send home a returning guest using a confirmation from its previous visit', async () => {
    const p = profile();
    const guest = {
      neighbor_id: 'door',
      label: 'The bay',
      whale_name: 'Fin',
      whale_outfit: 'maid_classic',
      allow_hire: true,
      working: true,
      started_at_ms: 1000,
    } as const;
    const controls = {
      busy: false,
      refreshing: false,
      dispatch: vi.fn(),
      onPreview: vi.fn(),
      onDepart: vi.fn(),
      onRefresh: vi.fn(),
      onActivity: vi.fn(),
      onWallet: vi.fn(),
    };
    p.social.guests = [guest];
    const { user, rerender } = await renderWithProviders(<SocialPane profile={p} {...controls} />, {
      station: 'user',
      locale: 'en',
    });
    await user.click(screen.getByRole('button', { name: 'Pay wages and send home' }));
    const confirmation = within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Confirm',
    });
    expect(confirmation).toBeEnabled();
    rerender(
      <SocialPane
        {...controls}
        profile={{ ...p, social: { ...p.social, guests: [{ ...guest, started_at_ms: 3000 }] } }}
      />,
    );
    expect(confirmation).toBeDisabled();
    await user.click(confirmation);
    expect(controls.dispatch).not.toHaveBeenCalled();
  });
  it('keeps visits open when wages are capped and withdraws an outdated offer to hire', async () => {
    const p = profile();
    const guest = {
      neighbor_id: 'door',
      label: 'The bay',
      whale_name: 'Fin',
      whale_outfit: 'maid_classic',
      allow_hire: true,
      working: false,
      started_at_ms: 1000,
    } as const;
    p.social.guests = [guest];
    const controls = {
      busy: false,
      refreshing: false,
      dispatch: vi.fn(),
      onPreview: vi.fn(),
      onDepart: vi.fn(),
      onRefresh: vi.fn(),
      onActivity: vi.fn(),
      onWallet: vi.fn(),
    };
    const { user, rerender } = await renderWithProviders(<SocialPane profile={p} {...controls} />, {
      station: 'user',
      locale: 'en',
    });
    await user.click(screen.getByRole('button', { name: 'Hire as a helper' }));
    rerender(
      <SocialPane
        {...controls}
        profile={{ ...p, social: { ...p.social, guests: [{ ...guest, hire_blocked: 'quota' }] } }}
      />,
    );
    expect(
      within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Confirm' }),
    ).toBeDisabled();
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('button', { name: 'Hire as a helper' })).not.toBeInTheDocument();
    expect(screen.getByText('Daily social coin limit reached')).toBeVisible();
    expect(screen.getByRole('button', { name: 'Offer a meal' })).toBeEnabled();

    const peer = { id: 'door', gameProfilePublic: false, anonymousCode: '11' } as NeighborStation;
    rerender(
      <DepartureDialog
        busy={false}
        dailyWageLimitReached
        dispatch={controls.dispatch}
        neighbor={peer}
        onClose={vi.fn()}
      />,
    );
    expect(screen.getByText(/You can still visit/)).toBeVisible();
    expect(screen.getByRole('button', { name: 'Depart' })).toBeEnabled();
  });
  it.each(['en', 'zh'] as const)(
    'discloses the configured wage share and shared daily social limit before hiring in %s',
    async (locale) => {
      const p = profile();
      p.social.guests = [
        {
          neighbor_id: 'g',
          label: 'Guest',
          whale_name: 'Fin',
          whale_outfit: 'maid_classic',
          allow_hire: true,
          working: false,
          started_at_ms: 0,
        },
      ];
      const dispatch = vi.fn().mockResolvedValue(true);
      const { user } = await renderWithProviders(
        <SocialPane
          profile={p}
          busy={false}
          dispatch={dispatch}
          onPreview={vi.fn()}
          onDepart={vi.fn()}
          onRefresh={vi.fn()}
          refreshing={false}
          onActivity={vi.fn()}
          onWallet={vi.fn()}
        />,
        { station: 'user', locale },
      );
      await user.click(
        screen.getByRole('button', { name: locale === 'en' ? 'Hire as a helper' : '雇佣帮工' }),
      );
      const dialog = screen.getByRole('alertdialog');
      expect(dialog).toHaveAccessibleDescription(/20%.*7/);
      await user.click(
        within(dialog).getByRole('button', { name: locale === 'en' ? 'Confirm' : '确认' }),
      );
      expect(dispatch).toHaveBeenCalledWith(
        {
          type: 'catch_guest',
          neighborId: 'g',
          action: 'hire',
        },
        expect.any(Function),
      );
    },
  );
});
