import { expect, test, type BrowserContext } from '@playwright/test';
import { GOAL, HZ, LAST_TICK } from '../../src/user/games/steady-catch/engine';
import type { Controls, Session } from '../../src/user/games/steady-catch/session';
import { creditsToMilli } from '../../src/user/games/common/strict';
import { api, context, control, fixture } from './real-fixture';

const base = '/api/games/steady-catch';
async function current(ctx: BrowserContext): Promise<Session> {
  const response = await api(ctx, base + '/session');
  expect(response.status(), await response.text()).toBe(200);
  return response.json();
}
async function wallets(ctx: BrowserContext): Promise<{ balance: string; game_balance: string }> {
  const response = await api(ctx, '/api/games');
  expect(response.status()).toBe(200);
  return response.json();
}
async function controls(ctx: BrowserContext, session: Session, value: Controls): Promise<Session> {
  const response = await api(ctx, base + '/sessions/' + session.id + '/controls', 'POST', value);
  expect(response.status(), await response.text()).toBe(200);
  return response.json();
}

test('paid Catch survives pause and reload, confirms replacement, and saves a legal terminal', async ({
  browser,
}) => {
  const admin = await context(browser, true),
    ctx = await context(browser, false, 3),
    page = await ctx.newPage();
  try {
    const config = await (
      await api(admin, '/admin/api/games/config', 'GET', undefined, true)
    ).json();
    const configured = await api(
      admin,
      '/admin/api/games/config',
      'PATCH',
      {
        expected_revision: config.revision,
        master_enabled: true,
        steadycatch: { enabled: true, price: '2', first_clear_reward: '3' },
      },
      true,
    );
    expect(configured.status(), await configured.text()).toBe(200);
    const before = await wallets(ctx);
    await page.goto(fixture().user_url + '/games/steady-catch');
    await expect(page.getByText('No per-game credit rewards', { exact: true })).toBeVisible();
    const starting = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === base + '/sessions' &&
        response.request().method() === 'POST',
    );
    await page.getByRole('button', { name: 'Start catching', exact: true }).click();
    const firstResponse = await starting;
    expect(firstResponse.status()).toBe(200);
    const first = await current(ctx);
    expect(first.payment).toEqual({ general: '2', game: '0' });
    expect(creditsToMilli(before.balance) - creditsToMilli((await wallets(ctx)).balance)).toBe(
      2000n,
    );
    await expect(page.locator('.stage canvas')).toBeFocused();
    await page.keyboard.down('ArrowRight');
    await expect.poll(async () => (await current(ctx)).state.tick).toBeGreaterThanOrEqual(HZ);
    await page.keyboard.up('ArrowRight');
    await page.getByRole('button', { name: 'Pause game', exact: true }).click();
    await expect.poll(async () => (await current(ctx)).status).toBe('paused');
    const saved = await current(ctx);
    await page.reload();
    await expect(page.getByRole('button', { name: 'Keep catching', exact: true })).toBeEnabled();
    expect((await current(ctx)).state).toEqual(saved.state);
    await page.getByRole('button', { name: 'Keep catching', exact: true }).click();
    await expect.poll(async () => (await current(ctx)).status).toBe('playing');
    await page.getByRole('button', { name: 'Pause game', exact: true }).click();
    await expect.poll(async () => (await current(ctx)).status).toBe('paused');

    await page.getByRole('button', { name: 'Start a new game', exact: true }).click();
    const confirmation = page.getByRole('dialog', { name: 'Start a new game?', exact: true });
    await expect(confirmation).toContainText('without refund');
    const abandoning = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === base + '/sessions/' + first.id + '/controls' &&
        response.request().postDataJSON()?.action === 'abandon',
    );
    const replacement = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === base + '/sessions' &&
        response.request().method() === 'POST',
    );
    await confirmation.getByRole('button', { name: 'Start a new game', exact: true }).click();
    const abandonedResponse = await abandoning;
    expect(abandonedResponse.status()).toBe(200);
    // Read the persisted receipt through the same idempotent control request;
    // Chromium may discard network response bodies while the UI advances.
    const abandoned = await controls(
      ctx,
      first,
      abandonedResponse.request().postDataJSON() as Controls,
    );
    expect(abandoned.status).toBe('abandoned');
    expect(abandoned.reward).toBe('0');
    const nextResponse = await replacement;
    expect(nextResponse.status()).toBe(200);
    const next = await current(ctx);
    expect(next.id).not.toBe(first.id);
    expect(next.payment).toEqual(first.payment);
    await expect(page.getByRole('button', { name: 'Pause game', exact: true })).toBeEnabled();
    await page.getByRole('button', { name: 'Pause game', exact: true }).click();
    await expect.poll(async () => (await current(ctx)).status).toBe('paused');
    await page.goto(fixture().user_url + '/games');

    // Resume through the public controls route after detaching the UI writer.
    // The fixture advances only server time; the server computes every result.
    let terminal = await current(ctx);
    terminal = await controls(ctx, terminal, {
      revision: terminal.revision,
      action: 'resume',
      until_tick: terminal.state.tick,
      inputs: [],
    });
    await control(ctx, 'advance', { seconds: LAST_TICK / HZ });
    let finalInput: Controls | null = null;
    while (terminal.terminal_at === null && terminal.state.tick < LAST_TICK) {
      finalInput = {
        revision: terminal.revision,
        action: 'advance',
        until_tick: Math.min(terminal.state.tick + 300, LAST_TICK),
        inputs: [],
      };
      terminal = await controls(ctx, terminal, finalInput);
    }
    expect(['failed', 'completed']).toContain(terminal.status);
    expect(terminal.terminal_at).not.toBeNull();
    expect(['hp', 'time']).toContain(terminal.state.cause);
    if (terminal.status === 'completed') {
      expect(terminal.state.score).toBeGreaterThanOrEqual(GOAL);
      expect(terminal.first_clear).toBe(true);
      expect(terminal.reward).toBe('3');
    } else {
      expect(terminal.first_clear).toBe(false);
      expect(terminal.reward).toBe('0');
    }
    expect(finalInput).not.toBeNull();
    const replayed = await controls(ctx, terminal, finalInput!);
    expect(replayed.state).toEqual(terminal.state);
    expect(replayed.reward).toBe(terminal.reward);
    const after = await wallets(ctx);
    expect(creditsToMilli(before.balance) - creditsToMilli(after.balance)).toBe(4000n);
    expect(creditsToMilli(after.game_balance) - creditsToMilli(before.game_balance)).toBe(
      creditsToMilli(terminal.reward),
    );
    await page.goto(fixture().user_url + '/games/steady-catch');
    await expect(page.locator('.end-score')).toContainText(String(terminal.state.score));
    await expect(page.getByRole('button', { name: 'Catch again', exact: true })).toBeEnabled();
    expect((await current(ctx)).id).toBe(next.id);
  } finally {
    await ctx.close();
    await admin.close();
  }
});
