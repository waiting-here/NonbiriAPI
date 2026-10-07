import { expect, test, type BrowserContext, type FrameLocator, type Page } from '@playwright/test';
import type { AIBot } from '../../src/shared/aiPlayers';
import type { Action, Deck, View } from '../../src/user/games/gwent/types';
import { creditsToMilli } from '../../src/user/games/common/strict';
import { api, context, control, fixture } from './real-fixture';

const base = '/api/games/gwent';
interface Match {
  id: string;
  phase_seq: string;
  decision_id: string;
  content_hash: string;
  phase: string;
  view: View;
  own_payment: { general: string; game: string };
}
interface Result {
  id: string;
  outcome: 'win' | 'loss' | 'draw' | 'system_cancelled';
  reason: string;
  own_payment: { general: string; game: string };
  own_refund: { general: string; game: string };
  prize_general: string;
  ai: { first_clear: boolean; reward: string };
}
interface Home {
  current: Match | null;
  latest_result: Result | null;
}
interface Wallets {
  balance: string;
  game_balance: string;
}
interface AdminAI {
  settings: { enabled: boolean; revision: string };
  bots: AIBot[];
  policies: { id: string; definition: { faction: string } }[];
}
async function get<T>(ctx: BrowserContext, path: string, admin = false): Promise<T> {
  const response = await api(ctx, path, 'GET', undefined, admin);
  expect(response.status(), await response.text()).toBe(200);
  return response.json();
}
async function configure(admin: BrowserContext) {
  const config = await get<{ revision: string }>(admin, '/admin/api/games/config', true);
  const changed = await api(
    admin,
    '/admin/api/games/config',
    'PATCH',
    {
      expected_revision: config.revision,
      master_enabled: true,
      gwent: { enabled: true, modes: { standard: { enabled: false } } },
    },
    true,
  );
  expect(changed.status(), await changed.text()).toBe(200);
  const state = await get<AdminAI>(admin, '/admin/api/games/gwent/ai', true);
  expect(state.bots).toHaveLength(4);
  const enabled = await api(
    admin,
    '/admin/api/games/gwent/ai/settings',
    'POST',
    {
      ...state.settings,
      enabled: true,
    },
    true,
  );
  expect(enabled.status(), await enabled.text()).toBe(200);
  const bot = state.bots.find(
    (value) =>
      state.policies.find((p) => p.id === value.policy_id)?.definition.faction === 'deepseek',
  )!;
  expect(bot).toBeDefined();
  const saved = await api(
    admin,
    '/admin/api/games/gwent/ai/bots',
    'POST',
    {
      id: bot.id,
      expected_revision: bot.revision,
      name: bot.name,
      description: bot.description,
      enabled: true,
      policy_id: bot.policy_id,
      policy_version: bot.policy_version,
      ticket: '2',
      first_reward: '3',
      memory_days: bot.memory_days,
      memory_games: bot.memory_games,
      new_challenge: false,
    },
    true,
  );
  expect(saved.status(), await saved.text()).toBe(200);
  return bot.id;
}
async function observed(page: Page) {
  await page.addInitScript(() => {
    window.addEventListener('message', (event) => {
      if (
        event.origin !== location.origin ||
        event.source !== parent ||
        event.data?.channel !== 'nonbiri.gwent' ||
        event.data.type !== 'snapshot'
      )
        return;
      // Observe delivered identities without changing the native game or server state.
      (
        window as Window & { observedGwent?: { id: string; phaseSeq: string; decisionID: string } }
      ).observedGwent = event.data.snapshot.home?.current ?? undefined;
    });
  });
}
async function aligned(arena: FrameLocator, current: Match) {
  await expect
    .poll(() =>
      arena.locator('body').evaluate(() => {
        const value = (
          window as Window & {
            observedGwent?: { id: string; phaseSeq: string; decisionID: string };
          }
        ).observedGwent;
        return value ? [value.id, value.phaseSeq, value.decisionID] : null;
      }),
    )
    .toEqual([current.id, current.phase_seq, current.decision_id]);
}
async function actionUI(page: Page, arena: FrameLocator, current: Match, action: Action) {
  await aligned(arena, current);
  const submitted = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === base + '/sessions/' + current.id + '/actions' &&
      response.request().method() === 'POST',
  );
  if (current.view.choice) {
    const choice = current.view.choice;
    if (action.card) {
      const index = choice.cards.findIndex((card) => card.instance_id === action.card);
      expect(index).toBeGreaterThanOrEqual(0);
      await arena.locator('#dialog-cards button').nth(index).click();
    } else {
      const actions = current.view.legal_actions.filter((value) => !value.card);
      const index = actions.findIndex(
        (value) => value.kind === action.kind && value.row === action.row,
      );
      expect(index).toBeGreaterThanOrEqual(0);
      await arena.locator('#dialog-actions button').nth(index).click();
    }
  } else if (action.kind === 'pass') await arena.locator('#arena-pass').click();
  else if (action.kind === 'leader') await arena.locator('#arena-leader').click();
  else {
    const index = current.view.hand.findIndex((card) => card.instance_id === action.card);
    expect(index).toBeGreaterThanOrEqual(0);
    await arena.locator('#arena-hand button').nth(index).click();
    if (action.row) {
      const spy = current.view.hand[index].abilities.includes('spy');
      await arena.locator(`#arena-${spy ? 'op' : 'me'}-${action.row} .row-info`).click();
    }
  }
  const response = await submitted;
  expect(response.status(), await response.text()).toBe(200);
  const sent = response.request().postDataJSON();
  expect(sent).toEqual({
    phase_seq: expect.stringMatching(/^[1-9][0-9]*$/),
    decision_id: current.decision_id,
    action,
  });
  expect(BigInt(sent.phase_seq)).toBeGreaterThanOrEqual(BigInt(current.phase_seq));
}
function select(view: View): Action {
  if (view.choice) {
    if (view.choice.kind === 'mulligan' && view.choice.remaining === 2)
      return view.legal_actions.find((a) => a.kind === 'redraw') ?? view.legal_actions[0];
    return view.legal_actions.find((a) => a.kind === 'continue') ?? view.legal_actions[0];
  }
  const lead = view.board.reduce(
    (total, row) => total + (row.side === 'self' ? row.total : -row.total),
    0,
  );
  const play = view.legal_actions.find(
    (a) =>
      a.kind === 'play' &&
      view.hand.some(
        (c) => c.instance_id === a.card && c.power > 0 && !c.abilities.includes('spy'),
      ),
  );
  return lead > 0
    ? (view.legal_actions.find((a) => a.kind === 'pass') ?? play ?? view.legal_actions[0])
    : (play ?? view.legal_actions.find((a) => a.kind === 'pass') ?? view.legal_actions[0]);
}

test('original Gwent AI plays a paid legal match, restores results, resumes after restart, and keeps demo free', async ({
  browser,
}) => {
  test.setTimeout(120000);
  const admin = await context(browser, true),
    ctx = await context(browser, false, 1, 'zh'),
    page = await ctx.newPage();
  try {
    const botID = await configure(admin);
    const before = await get<Wallets>(ctx, '/api/games');
    const config = await get<{ gwent: { modes: { standard: { enabled: boolean } } } }>(
      ctx,
      '/api/games',
    );
    expect(config.gwent.modes.standard.enabled).toBe(false);
    const offers = await get<{
      enabled: boolean;
      bots: {
        terms_hash: string;
        terms: {
          content_hash: string;
          ticket: string;
          ai: { bot_id: string; first_reward: string; bot_loadout: Deck };
        };
      }[];
    }>(ctx, base + '/ai');
    expect(offers.enabled).toBe(true);
    const offer = offers.bots.find((value) => value.terms.ai.bot_id === botID)!;
    expect(offer.terms.ticket).toBe('2');
    expect(offer.terms.ai.first_reward).toBe('3');
    const catalog = await get<{ content_hash: string; modes: { ai: { cards: unknown[] } } }>(
      ctx,
      base + '/catalog',
    );
    expect(catalog.content_hash).toMatch(/^[a-f0-9]{64}$/);
    expect(catalog.modes.ai.cards.length).toBeGreaterThanOrEqual(216);
    await observed(page);
    await page.goto(fixture().user_url + '/games/gwent');
    const arena = page.frameLocator('iframe.gwent-original-frame');
    await expect(arena.locator('#lobby')).toBeVisible();
    for (const faction of ['openai', 'deepseek', 'claude', 'gemini']) {
      await arena.locator('#player-faction').selectOption(faction);
      expect(
        await arena
          .locator('#player-deck option')
          .evaluateAll((options) => options.map((o) => (o as HTMLOptionElement).value)),
      ).toEqual([
        'standard-balanced',
        'standard-resource',
        'standard-bond',
        'standard-control',
        'random-preset',
        'random-theme',
      ]);
    }
    await arena.locator('#player-deck').selectOption('standard-bond');
    await arena.locator('#opponent-faction').selectOption(botID);
    await expect(arena.locator('#opponent-deck')).toBeDisabled();
    await expect(arena.locator('#platform-terms')).toContainText('没有逐局积分奖励');
    await arena.locator('#opponent-deck-help').click();
    await expect(arena.locator('#dialog-title')).toContainText('冻结卡组');
    const cardsShown = offer.terms.ai.bot_loadout.cards.reduce((n, entry) => n + entry.count, 1);
    await expect(arena.locator('#dialog-cards .arena-card')).toHaveCount(cardsShown);
    await arena.locator('#dialog-actions button').click();
    const queued = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === base + '/queue' &&
        response.request().method() === 'POST',
    );
    await arena.locator('#launch-play').click();
    const queueResponse = await queued;
    expect(queueResponse.status()).toBe(202);
    expect(queueResponse.request().postDataJSON()).toMatchObject({
      mode: 'ai',
      bot_id: botID,
      expected_terms_hash: offer.terms_hash,
      loadout: { faction: 'gemini' },
    });
    await expect
      .poll(async () => (await get<Home>(ctx, base + '/state')).current?.id ?? null)
      .not.toBeNull();
    const admitted = (await get<Home>(ctx, base + '/state')).current!;
    expect(admitted.content_hash).toBe(offer.terms.content_hash);
    expect(
      creditsToMilli(admitted.own_payment.general) + creditsToMilli(admitted.own_payment.game),
    ).toBe(2000n);
    const paid = await get<Wallets>(ctx, '/api/games');
    expect(creditsToMilli(before.balance) - creditsToMilli(paid.balance)).toBe(
      creditsToMilli(admitted.own_payment.general),
    );
    expect(creditsToMilli(before.game_balance) - creditsToMilli(paid.game_balance)).toBe(
      creditsToMilli(admitted.own_payment.game),
    );
    const deadline = Date.now() + 65000;
    const decisions = new Set<string>();
    let result: Result | null = null,
      plays = 0,
      choices = 0,
      swaps = 0;
    for (let step = 0; step < 80 && Date.now() < deadline; step++) {
      const state = await get<Home>(ctx, base + '/state');
      if (!state.current) {
        result = state.latest_result;
        break;
      }
      const current = state.current;
      if (!current.view.legal_actions.length) {
        await control(ctx, 'advance', { seconds: 1 });
        await expect
          .poll(async () => {
            const next = await get<Home>(ctx, base + '/state');
            return (
              !next.current ||
              next.current.phase_seq !== current.phase_seq ||
              next.current.view.legal_actions.length > 0
            );
          })
          .toBe(true);
        continue;
      }
      const action = select(current.view);
      if (action.kind === 'play') plays++;
      if (current.view.choice) {
        choices++;
        decisions.add(current.decision_id);
      }
      if (action.kind === 'redraw') swaps++;
      await test.step(`${current.phase_seq} ${current.view.choice?.kind ?? 'turn'} ${JSON.stringify(action)}`, async () => {
        await actionUI(page, arena, current, action);
      });
    }
    expect(
      result,
      'A bounded sequence of real legal UI actions must reach a normal terminal',
    ).not.toBeNull();
    expect(result!.id).toBe(admitted.id);
    expect(['win', 'loss', 'draw']).toContain(result!.outcome);
    expect(plays).toBeGreaterThan(0);
    expect(choices).toBeGreaterThan(0);
    expect(decisions.size).toBeGreaterThan(1);
    expect(swaps).toBe(1);
    expect(result!.prize_general).toBe('0');
    const reward = result!.outcome === 'win' ? '3' : '0';
    expect(result!.ai.reward).toBe(reward);
    expect(result!.ai.first_clear).toBe(result!.outcome === 'win');
    const after = await get<Wallets>(ctx, '/api/games');
    expect(creditsToMilli(before.balance) - creditsToMilli(after.balance)).toBe(
      creditsToMilli(admitted.own_payment.general),
    );
    expect(creditsToMilli(after.game_balance) - creditsToMilli(before.game_balance)).toBe(
      creditsToMilli(reward) - creditsToMilli(admitted.own_payment.game),
    );
    await page.reload();
    await expect(arena.locator('#result-overlay')).toBeVisible();
    await expect(arena.locator('#result-subtitle')).toContainText(
      result!.outcome === 'win' ? '首次胜利奖励 3' : '没有逐局奖励',
    );
    await arena.locator('#return-lobby').click();
    await arena.locator('#platform-history').click();
    await expect(arena.locator('.platform-history-row')).toHaveCount(1);
    const replayed = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === base + '/history/' + admitted.id &&
        response.request().method() === 'GET',
    );
    await arena
      .locator('.platform-history-row')
      .first()
      .getByRole('button', { name: '回放', exact: true })
      .click();
    expect((await replayed).status()).toBe(200);
    await expect(arena.locator('.platform-replay-controls')).toBeVisible();
    await arena
      .locator('.platform-replay-controls')
      .getByRole('button', { name: '下一步', exact: true })
      .click();
    await expect(arena.locator('#battle-status')).toContainText('回放');
    await arena
      .locator('.platform-replay-controls')
      .getByRole('button', { name: '返回大厅', exact: true })
      .click();
    const secondQueue = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === base + '/queue' &&
        response.request().method() === 'POST',
    );
    await arena.locator('#launch-play').click();
    expect((await secondQueue).status()).toBe(202);
    await expect
      .poll(async () => (await get<Home>(ctx, base + '/state')).current?.id ?? null)
      .not.toBeNull();
    const second = (await get<Home>(ctx, base + '/state')).current!;
    await page.goto(fixture().user_url + '/games');
    await control(ctx, 'restart');
    const resumed = (await get<Home>(ctx, base + '/state')).current!;
    expect(resumed.id).toBe(second.id);
    expect(resumed.own_payment).toEqual(second.own_payment);
    expect(resumed.decision_id).not.toBe(second.decision_id);
    const secondPaid = await get<Wallets>(ctx, '/api/games');
    expect(creditsToMilli(after.balance) - creditsToMilli(secondPaid.balance)).toBe(
      creditsToMilli(second.own_payment.general),
    );
    expect(creditsToMilli(after.game_balance) - creditsToMilli(secondPaid.game_balance)).toBe(
      creditsToMilli(second.own_payment.game),
    );
    await page.goto(fixture().user_url + '/games/gwent');
    for (let step = 0; step < 8; step++) {
      const current = (await get<Home>(ctx, base + '/state')).current!;
      await aligned(arena, current);
      if (!current.view.choice) break;
      await actionUI(page, arena, current, select(current.view));
    }
    await expect(arena.locator('#arena-dialog')).not.toBeVisible();
    await arena.locator('#arena-concede').click();
    const surrendered = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === base + '/sessions/' + second.id + '/surrender' &&
        response.request().method() === 'POST',
    );
    await arena
      .locator('#dialog-actions')
      .getByRole('button', { name: '认输', exact: true })
      .click();
    expect((await surrendered).status()).toBe(200);
    await expect(arena.locator('#result-overlay')).toBeVisible();
    const lost = (await get<Home>(ctx, base + '/state')).latest_result!;
    expect(lost.id).toBe(second.id);
    expect(lost.outcome).toBe('loss');
    expect(lost.ai.reward).toBe('0');
    expect(lost.own_refund).toEqual({ general: '0', game: '0' });
    expect(await get<Wallets>(ctx, '/api/games')).toMatchObject({
      balance: secondPaid.balance,
      game_balance: secondPaid.game_balance,
    });
    await arena.locator('#return-lobby').click();
    const historyBefore = await get<{ items: Result[] }>(ctx, base + '/history');
    const writes: string[] = [];
    page.on('request', (request) => {
      if (
        request.method() === 'POST' &&
        /^\/api\/games\/gwent\/(queue|sessions\/[^/]+\/(actions|surrender))$/.test(
          new URL(request.url()).pathname,
        )
      )
        writes.push(request.url());
    });
    await arena.locator('[data-mode="demo"]').click();
    await expect(arena.locator('#platform-terms')).toContainText('免费本机演示');
    await arena.locator('#launch-watch').click();
    await expect(arena.locator('#battle')).toBeVisible();
    await arena.locator('#arena-speed').selectOption('0.12');
    await expect(arena.locator('#result-overlay')).toBeVisible({ timeout: 30000 });
    expect(writes).toEqual([]);
    expect(await get<Wallets>(ctx, '/api/games')).toMatchObject({
      balance: secondPaid.balance,
      game_balance: secondPaid.game_balance,
    });
    expect(await get<{ items: Result[] }>(ctx, base + '/history')).toEqual(historyBefore);
  } finally {
    await ctx.close();
    await admin.close();
  }
});
