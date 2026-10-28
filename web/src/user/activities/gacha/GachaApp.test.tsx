import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { GachaApp } from './GachaApp';
import { Shop } from './Shop';
import { themes } from './catalog';
import { ArtworkPolicyProvider } from '@shared/artwork/policy';
import { formatDateTime } from '@shared/utils/datetime';
import { gachaSnapshot } from '../../../../test/fixtures/gacha';
import type { GachaPort, Outcome, Snapshot } from './types';

function snapshot(): Snapshot {
  return { ...gachaSnapshot(), visible_themes: themes.map((theme) => theme.id) };
}

it.each([
  ['never offered', ['g'], ['g-chatgpt'], false, 1, 5],
  ['previously offered', ['g', 'i'], ['i-chatgpt'], false, 1, 10],
  ['owned limited theme', ['g', 'l'], ['l-chatgpt'], false, 1, 6],
  ['rated artwork hidden', ['g', 's'], ['g-chatgpt', 's-chatgpt'], true, 1, 5],
  ['rated artwork allowed', ['g', 's'], ['g-chatgpt', 's-chatgpt'], false, 2, 7],
] as const)(
  'uses the same collection progress on the home and atlas pages: %s',
  async (name, visibleThemes, owned, disabled, collected, total) => {
    const state = gachaSnapshot();
    state.visible_themes = [...visibleThemes];
    for (const id of owned) state.owned[id] = { at: state.server_time, source: 'draw' };
    const port: GachaPort = {
      scope: 'collection-progress:' + name,
      load: async () => state,
      act: async () => ({ snapshot: state }),
      history: async () => ({ items: [] }),
    };
    const view = render(
      <ArtworkPolicyProvider disabled={disabled}>
        <GachaApp port={port} english />
      </ArtworkPolicyProvider>,
    );
    const open = await screen.findByRole('button', { name: /Open your collection/ });
    expect(within(open).getByText(`${collected} / ${total}`, { exact: true })).toBeVisible();
    fireEvent.click(open);
    expect(view.container.querySelector('.gacha-section-heading')).toHaveTextContent(
      `${collected} / ${total} collected`,
    );
  },
);

it.each([false, true])(
  'updates collection progress when artwork policy changes and retains restricted placeholders, English=%s',
  async (english) => {
    const state = gachaSnapshot();
    state.visible_themes = ['g', 's'];
    for (const id of ['g-chatgpt', 's-chatgpt'])
      state.owned[id] = { at: state.server_time, source: 'draw' };
    const port: GachaPort = {
      scope: 'collection-policy:' + english,
      load: async () => state,
      act: async () => ({ snapshot: state }),
      history: async () => ({ items: [] }),
    };
    const tree = (disabled: boolean) => (
      <ArtworkPolicyProvider disabled={disabled}>
        <GachaApp port={port} english={english} />
      </ArtworkPolicyProvider>
    );
    const view = render(tree(false));
    const label = english ? /Open your collection/ : /翻开收藏册/;
    const note = english
      ? 'Restricted cards are not included in collection progress.'
      : '未开放的分级卡牌不计入收藏进度。';
    expect(
      within(await screen.findByRole('button', { name: label })).getByText('2 / 7'),
    ).toBeVisible();
    expect(screen.queryByText(note)).not.toBeInTheDocument();
    view.rerender(tree(true));
    const open = screen.getByRole('button', { name: label });
    expect(within(open).getByText('1 / 5')).toBeVisible();
    expect(screen.getByText(note)).toBeVisible();
    fireEvent.click(open);
    expect(view.container.querySelector('.gacha-collection-progress')).toHaveTextContent('1 / 5');
    expect(view.container.querySelectorAll('.gacha-catalog .gacha-card')).toHaveLength(7);
    expect(view.container.querySelectorAll('.gacha-catalog .nb-artwork-placeholder')).toHaveLength(
      2,
    );
    expect(screen.getByText(note)).toBeVisible();
    view.rerender(tree(false));
    expect(view.container.querySelector('.gacha-collection-progress')).toHaveTextContent('2 / 7');
    expect(screen.queryByText(note)).not.toBeInTheDocument();
    expect(view.container.querySelectorAll('.gacha-catalog .nb-artwork-placeholder')).toHaveLength(
      0,
    );
  },
);

it.each([
  ['-1', '10000', '10000', '10,000 游戏积分 + 0 通用积分', true, false],
  ['10000', '-0.001', '10000', '0 游戏积分 + 10,000 通用积分', true, true],
  ['-1', '-0.001', '10000', '0 游戏积分 + 10,000 通用积分', false, false],
  ['0', '0', '10000', '0 游戏积分 + 10,000 通用积分', false, false],
  ['0.999', '0.001', '1', '0.001 游戏积分 + 0.999 通用积分', true, false],
] as const)(
  'renders wallets %s/%s and previews ticket payment without spending debt',
  async (general, game, price, split, standardAllowed, limitedAllowed) => {
    const state = snapshot();
    state.credits = { general, game };
    state.shop.permanent_price = state.shop.limited_price = price;
    const port: GachaPort = {
      scope: `user:wallet:${general}:${game}`,
      load: async () => state,
      act: async () => ({ snapshot: state }),
      history: async () => ({ items: [] }),
    };
    render(<GachaApp port={port} />);
    fireEvent.click(await screen.findByTitle('兑换常驻抽奖券'));
    const dialog = within(screen.getByRole('dialog', { name: '兑换抽奖券' }));
    fireEvent.change(dialog.getByRole('spinbutton', { name: '兑换数量' }), {
      target: { value: '1' },
    });
    expect(dialog.getByText('优先消耗游戏积分：' + split)).toBeVisible();
    for (const balance of [general, game].filter((value) => value.startsWith('-'))) {
      expect(dialog.getByText(balance + ' 积分')).toBeVisible();
    }
    const standard = dialog.getByRole('button', {
      name: standardAllowed ? '兑换 1 张' : '积分不足',
    });
    if (standardAllowed) expect(standard).toBeEnabled();
    else expect(standard).toBeDisabled();
    fireEvent.click(dialog.getByRole('tab', { name: '限定抽奖券' }));
    expect(dialog.getByText('仅消耗通用积分。')).toBeVisible();
    const limited = dialog.getByRole('button', { name: limitedAllowed ? '兑换 1 张' : '积分不足' });
    if (limitedAllowed) expect(limited).toBeEnabled();
    else expect(limited).toBeDisabled();
  },
);

it('keeps rendering when an authoritative purchase response changes a wallet below zero', async () => {
  const state = snapshot();
  const act = vi.fn<GachaPort['act']>(async (input) => {
    if (input.action === 'buy') {
      state.credits = { general: '-0.001', game: '10000' };
      state.tickets.permanent++;
    }
    return { snapshot: structuredClone(state) };
  });
  render(
    <GachaApp
      port={{
        scope: 'user:purchase-balance',
        load: async () => state,
        act,
        history: async () => ({ items: [] }),
      }}
    />,
  );
  fireEvent.click(await screen.findByTitle('兑换常驻抽奖券'));
  fireEvent.change(screen.getByRole('spinbutton', { name: '兑换数量' }), {
    target: { value: '1' },
  });
  fireEvent.click(screen.getByRole('button', { name: '兑换 1 张' }));
  await waitFor(() =>
    expect(act).toHaveBeenCalledWith(
      { action: 'buy', ticket: 'permanent', quantity: 1, revision: state.shop.revision },
      expect.any(String),
    ),
  );
  fireEvent.click(await screen.findByTitle('兑换限定抽奖券'));
  expect(await screen.findByText('-0.001 积分')).toBeVisible();
  expect(screen.getByRole('button', { name: '积分不足' })).toBeDisabled();
});

it('keeps entry identity through a lost response and language change', async () => {
  const act = vi
    .fn<GachaPort['act']>()
    .mockRejectedValueOnce(new TypeError('response lost'))
    .mockResolvedValue({ snapshot: snapshot() });
  const port: GachaPort = {
    scope: 'user:entry-retry',
    act,
    load: async () => snapshot(),
    history: async () => ({ items: [] }),
  };
  const view = render(<GachaApp port={port} />);
  fireEvent.click(await screen.findByRole('button', { name: '重试' }));
  await screen.findByRole('button', { name: '签到 · 常驻券 +1' });
  expect(act).toHaveBeenCalledTimes(2);
  expect(act.mock.calls[0][1]).toBe(act.mock.calls[1][1]);
  view.rerender(<GachaApp port={port} english />);
  expect(await screen.findByRole('button', { name: /Check in.*standard/i })).toBeEnabled();
  expect(act).toHaveBeenCalledTimes(2);
});

it.each([
  [false, false],
  [true, false],
  [false, true],
  [true, true],
])(
  'explains an upcoming limited pool and respects its time boundaries, English=%s paused=%s',
  async (english, paused) => {
    const state = snapshot();
    const localNow = 1_700_000_000_000;
    state.tickets.limited = 10;
    state.pools.push({
      ...state.pools[0],
      id: 'future',
      name: '未来奖池',
      name_en: 'Future pool',
      kind: 'limited',
      cover: 'i',
      group: 'future',
      featured: ['i-chatgpt'],
      starts_at: state.server_time + 60,
      ends_at: state.server_time + 120,
      paused,
    });
    const action = vi.fn<GachaPort['act']>(async () => ({ snapshot: state }));
    const clock = vi.spyOn(Date, 'now').mockReturnValue(localNow);
    const originalLang = document.documentElement.lang;
    document.documentElement.lang = english ? 'en' : 'zh';
    try {
      render(
        <GachaApp
          port={{
            scope: `upcoming:${english}:${paused}`,
            load: async () => state,
            act: action,
            history: async () => ({ items: [] }),
          }}
          initialPoolId="future"
          english={english}
        />,
      );

      const once = await screen.findByRole('button', {
        name: english ? /Summon once/ : /召唤一次/,
      });
      const ten = screen.getByRole('button', { name: english ? /Summon ten/ : /十次召唤/ });
      const status = english ? 'LIMITED · NOT STARTED' : '限定召唤 · 尚未开始';
      const notice = english
        ? 'Pool not started yet. Summoning unavailable.'
        : '奖池尚未开始，暂不可召唤。';
      expect(screen.getByText(status)).toBeVisible();
      expect(
        screen.getByText(
          (english ? 'Starts ' : '开始时间 ') +
            formatDateTime(state.pools[1].starts_at).replace(/\s+/g, ' '),
        ),
      ).toBeVisible();
      expect(screen.getByText(notice)).toBeVisible();
      expect(once).toBeDisabled();
      expect(ten).toBeDisabled();
      expect(once).toHaveAttribute('aria-describedby', 'gacha-upcoming-notice');
      expect(ten).toHaveAttribute('aria-describedby', 'gacha-upcoming-notice');

      clock.mockReturnValue(localNow + 59_000);
      fireEvent.focus(window);
      expect(screen.getByText(status)).toBeVisible();
      expect(ten).toBeDisabled();

      clock.mockReturnValue(localNow + 60_000);
      fireEvent.focus(window);
      expect(screen.queryByText(status)).not.toBeInTheDocument();
      expect(screen.queryByText(notice)).not.toBeInTheDocument();
      if (paused) {
        expect(once).toBeDisabled();
        expect(ten).toBeDisabled();
      } else {
        expect(once).toBeEnabled();
        expect(ten).toBeEnabled();
      }

      clock.mockReturnValue(localNow + 120_000);
      fireEvent.focus(window);
      expect(screen.queryByText(status)).not.toBeInTheDocument();
      expect(once).toBeDisabled();
      expect(ten).toBeDisabled();
      expect(action).toHaveBeenCalledTimes(1);
    } finally {
      document.documentElement.lang = originalLang;
      clock.mockRestore();
    }
  },
);

it.each([false, true])(
  'labels every shop card with its actual shard cost, English=%s',
  (english) => {
    const state = snapshot();
    state.pools.push({
      ...state.pools[0],
      id: 'limited',
      kind: 'limited',
      starts_at: state.server_time - 60,
      ends_at: state.server_time + 60,
      featured: ['h-chatgpt', 'j-chatgpt', 'l-chatgpt'],
    });
    const view = render(
      <Shop state={state} english={english} busy={false} act={vi.fn()} inspect={vi.fn()} />,
    );
    const prices = [...view.container.querySelectorAll('.gacha-shop-card__price')].map(
      (node) => node.textContent,
    );
    if (english) {
      expect(prices).toContain('Cost: 30 N shards and 15 R shards');
      expect(prices).toContain('Cost: 10 SR shards');
      expect(prices).toContain('Cost: 3 SSR shards');
      expect(prices).toContain('Cost: 9 SSR shards');
    } else {
      expect(prices).toContain('兑换需要：30 N 碎片 和 15 R 碎片');
      expect(prices).toContain('兑换需要：10 SR 碎片');
      expect(prices).toContain('兑换需要：3 SSR 碎片');
      expect(prices).toContain('兑换需要：9 SSR 碎片');
    }
  },
);
it('claims both daily tickets independently', async () => {
  const state = snapshot();
  const port: GachaPort = {
    scope: 'user:both-daily-tickets',
    load: async () => structuredClone(state),
    history: async () => ({ items: [] }),
    act: async (input) => {
      if (input.action === 'check-in') {
        state.checked_in = true;
        state.tickets.permanent++;
      }
      if (input.action === 'limited-check-in') {
        state.limited_checked_in = true;
        state.tickets.limited++;
      }
      return { snapshot: structuredClone(state) };
    },
  };
  render(<GachaApp port={port} />);
  fireEvent.click(await screen.findByRole('button', { name: '签到 · 常驻券 +1' }));
  expect(await screen.findByRole('button', { name: '常驻券已领取' })).toBeDisabled();
  fireEvent.click(screen.getByRole('button', { name: '签到 · 限定券 +1' }));
  expect(await screen.findByRole('button', { name: '限定券已领取' })).toBeDisabled();
  expect(state.tickets).toEqual({ permanent: 11, limited: 1 });
});

beforeEach(() => {
  localStorage.setItem(
    'nb.gacha.preferences',
    JSON.stringify({ music: false, sound: false, reduced: true }),
  );
});

it.each(['draw', 'shop', 'reward'])(
  'a single %s acquisition unlocks both replay choices without a mutation',
  async (source) => {
    const state = snapshot();
    state.owned['n-gemini'] = { at: state.server_time, source };
    const act = vi.fn<GachaPort['act']>(async () => ({ snapshot: state }));
    render(
      <GachaApp
        port={{
          scope: 'replay:' + source,
          load: async () => state,
          act,
          history: async () => ({ items: [] }),
        }}
        english
      />,
    );
    fireEvent.click(await screen.findByRole('tab', { name: /^Collection/ }));
    fireEvent.click(screen.getByRole('button', { name: /Across the Mirror Sea · Gemini/ }));
    expect(screen.getByRole('button', { name: 'Replay first encounter' })).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Replay reunion' })).toBeEnabled();
    const calls = act.mock.calls.length;
    fireEvent.click(screen.getByRole('button', { name: 'Replay reunion' }));
    expect(await screen.findByText('Reunion memory')).toBeVisible();
    expect(screen.queryByText(/Together again · \+1/)).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Close replay' }));
    expect(act).toHaveBeenCalledTimes(calls);
  },
);

it('retries a lost draw response with the original operation after remounting', async () => {
  const state = snapshot(),
    keys: string[] = [];
  const receipt = {
    id: 'receipt',
    pool_id: 'permanent',
    pool_name: '永续智能',
    created_at: state.server_time,
    items: [{ card_id: 'g-chatgpt', duplicate: false, guarantee: 'newcomer' as const }],
  };
  let committed = false;
  const port: GachaPort = {
    scope: 'user:retry-player',
    load: vi.fn(async () => structuredClone(state)),
    history: async () => ({ items: [] }),
    act: vi.fn(async (input, key) => {
      if (input.action === 'draw') {
        keys.push(key);
        if (!committed) {
          committed = true;
          state.tickets.permanent -= 10;
          throw new Error('response lost after commit');
        }
        return { snapshot: structuredClone(state), draw: receipt };
      }
      return { snapshot: structuredClone(state) };
    }),
  };
  const first = render(<GachaApp port={port} />);
  fireEvent.click(await screen.findByRole('button', { name: /十次召唤/ }));
  expect(await screen.findByRole('button', { name: '重试原操作' })).toBeEnabled();
  expect(state.tickets.permanent).toBe(0);
  first.unmount();
  render(<GachaApp port={port} />);
  fireEvent.click(await screen.findByRole('button', { name: '重试原操作' }));
  expect(await screen.findByRole('dialog', { name: '星光回应了你的期待' })).toBeVisible();
  expect(keys).toHaveLength(2);
  expect(keys[0]).toBe(keys[1]);
  expect(state.tickets.permanent).toBe(0);
  expect(sessionStorage.getItem('nb.gacha.pending:' + port.scope)).toBeNull();
});

it('does not reveal a late result or reuse pending work for a different account', async () => {
  const state = snapshot();
  let complete!: (value: Outcome) => void;
  const oldPort: GachaPort = {
    scope: 'user:first',
    load: async () => state,
    history: async () => ({ items: [] }),
    act: vi.fn(async (input) =>
      input.action === 'draw'
        ? new Promise<Outcome>((resolve) => {
            complete = resolve;
          })
        : { snapshot: state },
    ),
  };
  const first = render(<GachaApp port={oldPort} />);
  fireEvent.click(await screen.findByRole('button', { name: /十次召唤/ }));
  await waitFor(() => expect(complete).toBeTypeOf('function'));
  first.unmount();
  const next: GachaPort = {
    scope: 'user:second',
    load: async () => state,
    history: async () => ({ items: [] }),
    act: vi.fn(async () => ({ snapshot: state })),
  };
  render(<GachaApp port={next} />);
  await screen.findByRole('button', { name: /十次召唤/ });
  complete({
    snapshot: {
      ...state,
      visible_themes: themes.map((theme) => theme.id),
      tickets: { permanent: 0, limited: 0 },
    },
  });
  await waitFor(() => expect(next.act).toHaveBeenCalledTimes(1));
  expect(vi.mocked(next.act).mock.calls[0][0]).toEqual({ action: 'enter' });
  expect(screen.queryByRole('button', { name: '重试原操作' })).not.toBeInTheDocument();
  expect(screen.getByRole('button', { name: /十次召唤/ })).toBeEnabled();
});

it.each([false, true])(
  'offers repeat draws only with tickets and an open original pool (paused=%s)',
  async (paused) => {
    const state = snapshot(),
      keys: string[] = [];
    state.tickets.permanent = 20;
    const port: GachaPort = {
      scope: 'user:repeat-' + paused,
      load: async () => structuredClone(state),
      history: async () => ({ items: [] }),
      act: vi.fn(async (input, key) => {
        if (input.action !== 'draw') return { snapshot: structuredClone(state) };
        keys.push(key);
        expect(input).toEqual({ action: 'draw', pool_id: 'permanent', quantity: 10 });
        state.tickets.permanent -= 10;
        state.pools[0].paused = paused;
        return {
          snapshot: structuredClone(state),
          draw: {
            id: key,
            pool_id: 'permanent',
            pool_name: '永续智能',
            created_at: state.server_time,
            items: Array.from({ length: 10 }, () => ({
              card_id: 'a-chatgpt',
              duplicate: true,
              guarantee: 'none' as const,
            })),
          },
        };
      }),
    };
    render(<GachaApp port={port} />);
    fireEvent.click(await screen.findByRole('button', { name: /十次召唤/ }));
    await screen.findByRole('dialog', { name: '此次相遇' });
    if (paused) {
      expect(screen.queryByRole('button', { name: '再来一次' })).not.toBeInTheDocument();
      expect(keys).toHaveLength(1);
    } else {
      fireEvent.click(screen.getByRole('button', { name: '再来一次' }));
      await waitFor(() => expect(keys).toHaveLength(2));
      await screen.findByRole('dialog', { name: '此次相遇' });
      expect(keys[1]).not.toBe(keys[0]);
      expect(state.tickets.permanent).toBe(0);
      expect(screen.queryByRole('button', { name: '再来一次' })).not.toBeInTheDocument();
    }
  },
);
