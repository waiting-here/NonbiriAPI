import { expect, test, type BrowserContext, type FrameLocator, type Page } from '@playwright/test';
import type { Action, View } from '../../src/user/games/gwent/types';
import type { Match as CompanionMatch } from '../../src/user/games/companion/api';
import { api, context, fixture } from './real-fixture';

interface Match extends Omit<CompanionMatch, 'view'> {
  view: View;
}
const base = '/api/games/gwent/companion';

async function enableCompanion(admin: BrowserContext, game: 'bidding' | 'likes' | 'gwent') {
  const config = await (await api(admin, '/admin/api/games/config', 'GET', undefined, true)).json();
  const configWrite = await api(
    admin,
    '/admin/api/games/config',
    'PATCH',
    { expected_revision: config.revision, master_enabled: true, [game]: { enabled: true } },
    true,
  );
  expect(configWrite.ok(), await configWrite.text()).toBe(true);
  const enabled = await api(
    admin,
    '/admin/api/games/' + game + '/companion/settings',
    'POST',
    { enabled: true },
    true,
  );
  expect(enabled.ok(), await enabled.text()).toBe(true);
}

async function actionUI(page: Page, arena: FrameLocator, current: Match, action: Action) {
  await expect
    .poll(() => arena.locator('html').getAttribute('data-observed-decision'))
    .toBe(current.decision_id);
  const [response] = await Promise.all([
    page.waitForResponse(
      (r) =>
        new URL(r.url()).pathname === base + '/matches/' + current.id + '/actions' &&
        r.request().method() === 'POST',
    ),
    (async () => {
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
    })(),
  ]);
  expect(response.ok(), await response.text()).toBe(true);
  expect(response.request().postDataJSON()).toEqual({ decision_id: current.decision_id, action });
}

test('native Gwent deck selection, simultaneous decisions, reconnect and free settlement', async ({
  browser,
}) => {
  test.setTimeout(180000);
  const admin = await context(browser, true),
    user = await context(browser, false, 1, 'zh'),
    page = await user.newPage();
  try {
    await enableCompanion(admin, 'gwent');
    await page.addInitScript(() => {
      window.addEventListener('message', (event) => {
        if (event.data?.channel === 'nonbiri.gwent' && event.data.type === 'snapshot')
          document.documentElement.dataset.observedDecision =
            event.data.snapshot?.home?.current?.decisionID || '';
      });
    });
    const before = await (await api(user, '/api/games')).json();
    await page.goto(fixture().user_url + '/games/gwent');
    const arena = page.frameLocator('iframe.gwent-original-frame');
    await arena.locator('#companion-entry').click();
    await page.getByLabel('对手阵营', { exact: true }).selectOption('deepseek');
    await arena.locator('#player-faction').selectOption('claude');
    await arena.locator('#edit-deck').click();
    await expect(arena.locator('#deck-editor')).toBeVisible();
    await arena.locator('#deck-cancel').click();
    const [created] = await Promise.all([
      page.waitForResponse(
        (r) => new URL(r.url()).pathname === base + '/matches' && r.request().method() === 'POST',
      ),
      arena.locator('#launch-play').click(),
    ]);
    expect(created.ok(), await created.text()).toBe(true);
    expect(created.request().postDataJSON()).toMatchObject({
      constraints: { faction: 'deepseek' },
      setup: { faction: 'claude' },
    });
    const id = (await created.json()).id;
    await page.reload();
    await expect(arena.locator('#battle')).toBeVisible({ timeout: 30000 });
    let steps = 0;
    while (steps < 180) {
      let current!: Match;
      await expect
        .poll(
          async () => {
            const response = await api(user, base + '/matches/' + id);
            expect(response.ok(), await response.text()).toBe(true);
            current = await response.json();
            return current.status !== 'active' || Boolean(current.decision_id);
          },
          { timeout: 30000 },
        )
        .toBe(true);
      if (current.status !== 'active') {
        expect(current.status).toBe('completed');
        break;
      }
      const actions = current.view.legal_actions;
      const action =
        actions.find((a) => a.kind === 'continue') ??
        actions.find((a) => a.kind === 'pass') ??
        actions[0];
      expect(action).toBeTruthy();
      await actionUI(page, arena, current, action);
      steps++;
    }
    expect(steps).toBeGreaterThan(0);
    expect(steps).toBeLessThan(180);
    const after = await (await api(user, '/api/games')).json();
    expect([after.balance, after.game_balance]).toEqual([before.balance, before.game_balance]);
    await page.getByRole('button', { name: '陪玩记录' }).click();
    await page.getByRole('button', { name: '复盘', exact: true }).first().click();
    await expect(page.getByRole('dialog', { name: '陪玩记录' }).getByRole('slider')).toBeVisible();
  } finally {
    await user.close();
    await admin.close();
  }
});

for (const game of ['bidding', 'likes'] as const) {
  test(
    game + ' companion uses the original hand or plan controls and returns the saved match',
    async ({ browser }) => {
      test.setTimeout(90000);
      const admin = await context(browser, true),
        user = await context(browser, false, 1),
        page = await user.newPage();
      const base = '/api/games/' + game + '/companion';
      try {
        await enableCompanion(admin, game);
        const before = await (await api(user, '/api/games')).json();
        await page.goto(fixture().user_url + '/games/' + game);
        await page.getByRole('link', { name: 'Free AI companion' }).click();
        if (game === 'likes') {
          await page.getByText('Set opponent loadout', { exact: true }).click();
          await page.getByLabel('Opponent character', { exact: true }).selectOption({ index: 1 });
        }
        const [created] = await Promise.all([
          page.waitForResponse(
            (r) =>
              new URL(r.url()).pathname === base + '/matches' && r.request().method() === 'POST',
          ),
          page.getByRole('button', { name: 'Start free match' }).click(),
        ]);
        expect(created.ok(), await created.text()).toBe(true);
        if (game === 'likes')
          expect(created.request().postDataJSON().constraints.role).toBeTruthy();
        const id = (await created.json()).id;
        await page.reload();
        const path = base + '/matches/' + id + '/actions';
        const actionResponse = () =>
          page.waitForResponse(
            (r) => new URL(r.url()).pathname === path && r.request().method() === 'POST',
          );
        if (game === 'bidding') {
          const bid = page.getByRole('button', { name: /bid.*\(3\)/i });
          const joker = page.getByRole('button', { name: /save joker/i });
          await expect(bid.or(joker).first()).toBeVisible();
          if (await joker.isVisible()) {
            const [result] = await Promise.all([actionResponse(), joker.click()]);
            expect(result.ok(), await result.text()).toBe(true);
          }
          await bid.click();
        } else {
          await page.locator('button[data-guide^="cast:"]:enabled').first().click();
        }
        const lock =
          game === 'likes'
            ? page.locator('button[data-guide="lock"]')
            : page.getByRole('button', { name: /lock in bid/i });
        const [result] = await Promise.all([actionResponse(), lock.click()]);
        expect(result.ok(), await result.text()).toBe(true);
        expect(result.request().postDataJSON().action.kind).toBe(game === 'likes' ? 'plan' : 'bid');
        await page.getByRole('button', { name: 'Abandon match', exact: true }).click();
        await page
          .getByRole('alertdialog')
          .getByRole('button', { name: 'Abandon match', exact: true })
          .click();
        await expect(page.getByText('Match abandoned', { exact: true })).toBeVisible();
        const after = await (await api(user, '/api/games')).json();
        expect([after.balance, after.game_balance]).toEqual([before.balance, before.game_balance]);
      } finally {
        await user.close();
        await admin.close();
      }
    },
  );
}
