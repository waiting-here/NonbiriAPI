import { randomUUID } from 'node:crypto';
import { readFileSync } from 'node:fs';
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

test('real pond return keeps one receipt and current anonymity on both count boards', async ({
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
    for (let attempt = 0; attempt < 6; attempt += 1) {
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
    expect(batch, 'An eligible new junk catch from bounded real batches').toBeTruthy();
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
    const resource = `/api/games/fishing/batches/${batch!.batch_id}/outcomes/${target.ordinal}/return`;
    const returned = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === resource && response.request().method() === 'POST',
    );
    await item.getByRole('button', { name: 'Return to pond', exact: true }).click();
    const first = await returned;
    expect(first.status()).toBe(200);
    const receipt = await first.json();
    await expect(item).toContainText('Returned to pond');
    expect(await myCount()).toBe(before + 1n);
    await privacy(false);
    const repeat = await user.request.post(origin + resource, { headers: { Origin: origin } });
    expect(repeat.status()).toBe(200);
    expect(await repeat.json()).toEqual(receipt);
    expect(await myCount()).toBe(before + 1n);
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
