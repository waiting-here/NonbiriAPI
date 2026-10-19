import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { Shop } from './Shop';
import { GachaApp } from './GachaApp';
import { gachaSnapshot } from '../../../../test/fixtures/gacha';
import type { GachaPort } from './types';

it.each([false, true])(
  'shows daily bundle quantities, prices and independent availability, English=%s',
  (english) => {
    const state = gachaSnapshot();
    state.redeemed = ['bulk-permanent'];
    state.month_used = { permanent: 20, limited: 10 };
    const act = vi.fn();
    render(<Shop state={state} english={english} busy={false} act={act} inspect={vi.fn()} />);
    const shop = within(
      screen.getByRole('region', { name: english ? 'Bulk bargains' : '量大管饱' }),
    );
    expect(
      shop.getByRole('heading', { name: english ? '10 standard tickets' : '10张常驻抽奖券' }),
    ).toBeVisible();
    expect(
      shop.getByRole('heading', { name: english ? '10 limited tickets' : '10张限定抽奖券' }),
    ).toBeVisible();
    expect(shop.getAllByText(english ? '10,000 credits' : '10,000 悠哉积分')).toHaveLength(2);
    expect(shop.getByText(english ? 'Remaining today: 0/1' : '今日剩余：0/1')).toBeVisible();
    expect(shop.getByText(english ? 'Remaining today: 1/1' : '今日剩余：1/1')).toBeVisible();
    expect(
      shop.getByRole('button', { name: english ? 'Purchased today' : '今日已购买' }),
    ).toBeDisabled();
    fireEvent.click(shop.getByRole('button', { name: english ? 'Buy' : '购买' }));
    expect(act).toHaveBeenCalledWith(
      { action: 'redeem', product: 'bulk-limited', revision: 1 },
      expect.any(String),
    );
  },
);

it.each([
  ['-1', '10000', true],
  ['6000', '4000', true],
  ['5999.999', '4000', false],
  ['10000', '-1', true],
])('uses spendable combined credits for both bulk offers: %s/%s', (general, game, enabled) => {
  const state = gachaSnapshot();
  state.credits = { general, game };
  const view = render(
    <Shop state={state} english={false} busy={false} act={vi.fn()} inspect={vi.fn()} />,
  );
  const buttons = view.container.querySelectorAll('.gacha-bulk-shop button');
  expect(buttons).toHaveLength(2);
  for (const button of buttons) {
    if (enabled) expect(button).toBeEnabled();
    else expect(button).toBeDisabled();
  }
});

it('updates tickets and remaining availability after buying each bundle', async () => {
  localStorage.setItem(
    'nb.gacha.preferences',
    JSON.stringify({ music: false, sound: false, reduced: true }),
  );
  const state = gachaSnapshot();
  const act = vi.fn<GachaPort['act']>(async (input) => {
    if (input.action === 'redeem' && input.product.startsWith('bulk-')) {
      state.redeemed.push(input.product);
      const ticket = input.product === 'bulk-permanent' ? 'permanent' : 'limited';
      state.tickets[ticket] += 10;
      state.credits =
        input.product === 'bulk-permanent'
          ? { general: '10000', game: '0' }
          : { general: '0', game: '0' };
    }
    return { snapshot: structuredClone(state) };
  });
  render(
    <GachaApp
      port={{
        scope: 'user:bulk-shop',
        load: async () => structuredClone(state),
        act,
        history: async () => ({ items: [] }),
      }}
    />,
  );
  fireEvent.click(await screen.findByRole('tab', { name: '神秘商店' }));
  const shop = within(screen.getByRole('region', { name: '量大管饱' }));
  fireEvent.click(shop.getAllByRole('button', { name: '购买', exact: true })[0]);
  await waitFor(() => expect(shop.getAllByRole('button', { name: '今日已购买' })).toHaveLength(1));
  expect(screen.getByText('购买成功，10张抽奖券已到账')).toBeVisible();
  fireEvent.click(shop.getByRole('button', { name: '购买', exact: true }));
  await waitFor(() => expect(shop.getAllByRole('button', { name: '今日已购买' })).toHaveLength(2));
  expect(shop.getAllByText('今日剩余：0/1')).toHaveLength(2);
  expect(state.tickets).toEqual({ permanent: 20, limited: 10 });
});
