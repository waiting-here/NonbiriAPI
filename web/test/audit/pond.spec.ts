import { randomUUID } from 'node:crypto';
import { mkdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test, type Browser, type BrowserContext } from '@playwright/test';

type Cookie = { Name: string; Value: string };
type Fixture = {
  user_url: string;
  admin_url: string;
  admin_cookie: Cookie;
  users: { level: number; cookie: Cookie }[];
};
type Outcome = { ordinal: number; species_key: string; pond?: { return_state: string } };
type Batch = { batch_id: string; outcomes: Outcome[] };
type Settings = { revision: string; master_enabled: boolean; fishing: { enabled: boolean } };

async function session(browser: Browser, origin: string, cookie: Cookie, admin = false) {
  const context = await browser.newContext({
    viewport: { width: 390, height: 844 },
    reducedMotion: 'reduce',
  });
  await context.addCookies([
    {
      name: cookie.Name,
      value: cookie.Value,
      domain: new URL(origin).hostname,
      path: admin ? '/admin' : '/api',
      httpOnly: true,
      sameSite: 'Lax',
    },
  ]);
  await context.addInitScript(() => {
    localStorage.setItem('nb.lang', 'en');
    localStorage.setItem('nb.theme', 'dark');
  });
  return context;
}
async function api(
  context: BrowserContext,
  origin: string,
  path: string,
  method = 'GET',
  data?: unknown,
) {
  return context.request.fetch(origin + path, {
    method,
    data,
    headers: { Origin: origin, 'Idempotency-Key': randomUUID() },
  });
}

test('real pond batch return recovers a lost response and keeps single receipts and anonymity', async ({
  browser,
}) => {
  test.setTimeout(180_000);
  const fixture = JSON.parse(
    readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8'),
  ) as Fixture;
  const origin = fixture.user_url;
  const user = await session(
    browser,
    origin,
    fixture.users.find((user) => user.level === 1)!.cookie,
  );
  const admin = await session(browser, fixture.admin_url, fixture.admin_cookie, true);
  const configPath = '/admin/api/games/config';
  const readSettings = async () => {
    const response = await api(admin, fixture.admin_url, configPath);
    expect(response.status()).toBe(200);
    return response.json() as Promise<Settings>;
  };
  const original = await readSettings();
  const sessionResponse = await api(user, origin, '/api/session');
  const originalPublic = (
    (await sessionResponse.json()) as { user: { game_profile_public: boolean } }
  ).user.game_profile_public;
  const privacy = async (value: boolean) => {
    const response = await api(user, origin, '/api/me', 'PATCH', { game_profile_public: value });
    expect(response.status()).toBe(200);
  };
  const board = async (kind: 'cleanup' | 'gifts', period = '7d') => {
    const response = await api(
      user,
      origin,
      `/api/games/fishing/pond/leaderboard?board=${kind}&period=${period}`,
    );
    expect(response.status()).toBe(200);
    return response.json() as Promise<{
      entries: { item_count: string; is_me: boolean; identity: { kind: string } }[];
      me: { item_count: string; identity: { kind: string } } | null;
    }>;
  };
  const myCount = async () => {
    const value = await board('gifts');
    return BigInt(
      value.entries.find((row) => row.is_me)?.item_count ?? value.me?.item_count ?? '0',
    );
  };
  let batch: Batch | undefined;
  try {
    const enabled = await api(admin, fixture.admin_url, configPath, 'PATCH', {
      expected_revision: original.revision,
      master_enabled: true,
      fishing: { enabled: true },
    });
    expect(enabled.status(), await enabled.text()).toBe(200);
    await privacy(false);
    const before = await myCount();
    for (let attempt = 0; attempt < 8; attempt += 1) {
      const started = await api(user, origin, '/api/games/fishing/batches', 'POST', {
        bait: 'worm',
        count: 10,
      });
      expect([200, 202]).toContain(started.status());
      let settled: Batch | undefined;
      await expect
        .poll(
          async () => {
            const stateResponse = await api(user, origin, '/api/games/fishing/state');
            expect(stateResponse.status()).toBe(200);
            const state = (await stateResponse.json()) as { unrevealed: Batch | null };
            settled = state.unrevealed ?? undefined;
            return Boolean(settled);
          },
          { timeout: 20_000, intervals: [100, 300, 500] },
        )
        .toBe(true);
      if (
        settled!.outcomes.filter((outcome) => outcome.pond?.return_state === 'available').length >=
          2 &&
        settled!.outcomes.some(
          (outcome) => outcome.pond?.return_state === 'available' && outcome.species_key !== 'fry',
        )
      ) {
        batch = settled;
        break;
      }
      expect(
        (
          await api(user, origin, `/api/games/fishing/batches/${settled!.batch_id}/ack`, 'POST')
        ).status(),
      ).toBe(204);
    }
    expect(
      batch,
      'Multiple eligible new catches including junk from bounded real batches',
    ).toBeTruthy();
    const target = batch!.outcomes.find(
      (outcome) => outcome.pond?.return_state === 'available' && outcome.species_key !== 'fry',
    )!;
    const page = await user.newPage();
    await page.goto(origin + '/games/fishing');
    const item = page.locator(`.fishing-outcome[data-ordinal="${target.ordinal}"]`);
    await expect
      .poll(async () => {
        const state = await api(user, origin, '/api/games/fishing/state');
        return ((await state.json()) as { unrevealed: unknown }).unrevealed;
      })
      .toBeNull();
    const changedPrivacy = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === '/api/me' && response.request().method() === 'PATCH',
    );
    await page
      .locator('.fishing-pond-disclosure')
      .getByRole('switch', { name: 'Stay anonymous on leaderboards' })
      .click();
    expect((await changedPrivacy).status()).toBe(200);
    await expect(item.getByRole('button', { name: 'Return to pond', exact: true })).toBeEnabled();
    await expect(page.getByText(/The signature stays/)).toHaveCount(1);
    await expect(page).toHaveURL(origin + '/games/fishing');
    const resource = `/api/games/fishing/batches/${batch!.batch_id}/return`;
    const candidates = batch!.outcomes.filter(
      (outcome) => outcome.pond?.return_state === 'available',
    );
    const junkCount = BigInt(candidates.filter((outcome) => outcome.species_key !== 'fry').length);
    type Receipt = { ordinal: number; return_state: string; returned_at: number; batch_id: string };
    let committed: { batch_id: string; receipts: Receipt[] } | undefined;
    let dropResponse = true;
    await page.route('**' + resource, async (route) => {
      if (!dropResponse) {
        await route.continue();
        return;
      }
      dropResponse = false;
      const response = await route.fetch();
      expect(response.status()).toBe(200);
      committed = await response.json();
      await route.abort('failed');
    });
    const batchAction = page.locator('.fishing-pond-batch-return');
    await expect(batchAction.getByRole('button')).toBeEnabled();
    await batchAction.getByRole('button').click();
    await expect(batchAction).toContainText('Retry this batch to recover');
    await expect(item.getByRole('button', { name: 'Return to pond', exact: true })).toBeEnabled();
    expect(await myCount()).toBe(before + junkCount);
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: width === 390 ? 844 : 1000 });
      for (const theme of ['light', 'dark']) {
        await page.evaluate((value) => {
          document.documentElement.dataset.theme = value;
        }, theme);
        await batchAction.scrollIntoViewIfNeeded();
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
          true,
        );
        if (process.env.NONBIRI_VISUAL_DIR) {
          mkdirSync(process.env.NONBIRI_VISUAL_DIR, { recursive: true });
          await page.screenshot({
            path: join(process.env.NONBIRI_VISUAL_DIR, `pond-batch-retry-${width}-${theme}.png`),
          });
        }
      }
    }
    await privacy(false);
    const returned = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === resource && response.request().method() === 'POST',
    );
    await batchAction.getByRole('button', { name: 'Retry this batch', exact: true }).click();
    const recovered = await returned;
    expect(recovered.status()).toBe(200);
    const receipt = (await recovered.json()) as { batch_id: string; receipts: Receipt[] };
    expect(receipt).toEqual(committed);
    expect(receipt.receipts).toHaveLength(candidates.length);
    await expect(batchAction).toHaveCount(0);
    for (const value of receipt.receipts) {
      await expect(page.locator(`.fishing-outcome[data-ordinal="${value.ordinal}"]`)).toContainText(
        value.return_state === 'released' ? 'Released' : 'Returned to pond',
      );
    }
    const repeat = await user.request.post(origin + resource, { headers: { Origin: origin } });
    expect(repeat.status()).toBe(200);
    expect(await repeat.json()).toEqual(receipt);
    const singleResource = `/api/games/fishing/batches/${batch!.batch_id}/outcomes/${target.ordinal}/return`;
    const singleRepeat = await user.request.post(origin + singleResource, {
      headers: { Origin: origin },
    });
    expect(singleRepeat.status()).toBe(200);
    expect(await singleRepeat.json()).toEqual(
      receipt.receipts.find((value) => value.ordinal === target.ordinal),
    );
    expect(await myCount()).toBe(before + junkCount);
    for (const kind of ['cleanup', 'gifts'] as const) {
      const value = await board(kind);
      const mine = value.entries.find((row) => row.is_me) ?? value.me;
      expect(mine?.identity.kind).toBe('anonymous');
    }
    await page.getByRole('tab', { name: 'Gift leaderboard', exact: true }).click();
    await expect(page.locator('.fishing-pond-board')).toContainText('Anonymous angler');
    await page
      .locator('.fishing-pond-board')
      .getByRole('radiogroup')
      .getByText('All time', { exact: true })
      .click();
    await expect(page.locator('.fishing-pond-board .nb-table')).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
  } finally {
    try {
      await privacy(originalPublic);
      const current = await readSettings();
      const restored = await api(admin, fixture.admin_url, configPath, 'PATCH', {
        expected_revision: current.revision,
        master_enabled: original.master_enabled,
        fishing: { enabled: original.fishing.enabled },
      });
      expect.soft(restored.status(), await restored.text()).toBe(200);
    } finally {
      await user.close();
      await admin.close();
    }
  }
});
