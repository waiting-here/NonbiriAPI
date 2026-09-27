import { randomBytes } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { expect, test, type Browser, type BrowserContext, type Page } from '@playwright/test';

type Cookie = { Name: string; Value: string };
type Asset = 'general' | 'game' | 'sketch_paper' | 'sketch_brush';
interface Summary {
  metadata: { asset: Asset; ledger_seq: string; projected_seq: string };
  inventory: {
    user_available: string;
    negative_users: string;
    frozen: string;
    net: string;
  };
  reconciliation: { status: string; inventory_net: string; ledger_net: string };
}
interface Fixture {
  user_url: string;
  admin_url: string;
  users: { id: string; level: number; cookie: Cookie }[];
  admin_cookie: Cookie;
  request_ids: string[];
  discovery_id: string;
  diagnostic_id: string;
  image_task_id: string;
  projection_alert_id: string;
  issue_id: string;
  source_ip: string;
  source_client: string;
  json_body: string;
  text_body: string;
  private_markers: string[];
  summaries: Record<Asset, Summary>;
}
function fixture(): Fixture {
  return JSON.parse(readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8')) as Fixture;
}
async function session(browser: Browser, role: 'admin' | 1 | 5 | 6, mobile = false) {
  const f = fixture();
  const cookie = role === 'admin' ? f.admin_cookie : f.users.find((u) => u.level === role)!.cookie;
  const origin = role === 'admin' ? f.admin_url : f.user_url;
  const context = await browser.newContext({
    viewport: mobile ? { width: 390, height: 844 } : { width: 1280, height: 900 },
  });
  await context.addCookies([
    {
      name: cookie.Name,
      value: cookie.Value,
      domain: new URL(origin).hostname,
      path: role === 'admin' ? '/admin' : '/api',
      httpOnly: true,
      sameSite: 'Lax',
    },
  ]);
  await context.addInitScript(() => {
    if (!['http:', 'https:'].includes(location.protocol)) return;
    localStorage.setItem('nb.lang', 'en');
    localStorage.setItem('nb.theme', 'light');
  });
  return context;
}
async function api(
  context: BrowserContext,
  path: string,
  admin = false,
  method = 'GET',
  data?: unknown,
) {
  const origin = admin ? fixture().admin_url : fixture().user_url;
  return context.request.fetch(origin + path, {
    method,
    data,
    headers: { Origin: origin, 'Idempotency-Key': randomBytes(16).toString('base64url') },
  });
}
function safeResponses(page: Page) {
  const bodies: string[] = [];
  const pending: Promise<void>[] = [];
  // Navigation can cancel a response after its headers arrive. Only collect
  // completed requests, so an abandoned response body cannot stall the audit.
  page.on('requestfinished', (request) => {
    const path = new URL(request.url()).pathname;
    if (path.startsWith('/api/') || path.startsWith('/admin/api/')) {
      pending.push(
        request
          .response()
          .then((response) => response?.text())
          .then((body) => {
            if (body !== undefined) bodies.push(body);
          })
          .catch(() => {}),
      );
    }
  });
  return async () => {
    await Promise.all(pending);
    for (const body of bodies) {
      for (const marker of fixture().private_markers) expect(body).not.toContain(marker);
      for (const field of ['effective_ip', 'ip_quality', 'source_json', 'bytes_saved']) {
        expect(body).not.toContain('"' + field + '"');
      }
    }
  };
}
async function safeSink(page: Page) {
  expect(await page.locator('html').getAttribute('data-audit-executed')).toBeNull();
  await expect(page.locator('.request-diagnostics img, .request-diagnostics script')).toHaveCount(
    0,
  );
}

test('custom presets persist across reloads without matchmaking, payment or cross-owner disclosure', async ({
  browser,
}) => {
  const owner = await session(browser, 1);
  const other = await session(browser, 5);
  try {
    const page = await owner.newPage();
    const failures: string[] = [];
    page.on('pageerror', (error) => failures.push(error.message));
    const beforeResponse = await api(owner, '/api/games');
    expect(beforeResponse.status()).toBe(200);
    const before = (await beforeResponse.json()) as {
      balance: string;
      game_balance: string;
      likes: { plan_seconds: number };
    };
    expect(before.likes.plan_seconds).toBe(30);
    await page.goto(fixture().user_url + '/games/likes');
    const presets = page.getByRole('region', { name: 'Custom presets', exact: true });
    await expect(presets.getByRole('button', { name: /^Save to Preset\d+$/ })).toHaveCount(10);
    await page.locator('.likes-role-option').nth(1).click();
    await page.locator('.likes-harness-option [data-guide^="harness:"]').first().click();
    await presets.getByRole('button', { name: 'Save to Preset1', exact: true }).click();
    await expect(presets.getByRole('status')).toHaveText('Custom presets: Preset1 saved.');
    const savedResponse = await api(owner, '/api/games/likes/loadouts');
    expect(savedResponse.status()).toBe(200);
    const saved = (await savedResponse.json()) as {
      capacity: number;
      slots: {
        slot: number;
        revision: string;
        loadout: { role: string; harness: string; skills: string[] };
      }[];
    };
    expect(saved.capacity).toBe(10);
    expect(saved.slots).toHaveLength(1);
    expect(saved.slots[0].revision).toBe('1');
    expect(saved.slots[0].loadout.harness).not.toBeNull();
    await page.reload();
    await page.setViewportSize({ width: 390, height: 844 });
    await presets.getByRole('button', { name: 'Load Preset1', exact: true }).click();
    await expect(presets.getByRole('status')).toContainText(
      'loaded. You have not joined matchmaking.',
    );
    const selected = page.locator('.likes-role-option[aria-pressed="true"]');
    expect(await selected.getAttribute('data-guide')).toBe('role:' + saved.slots[0].loadout.role);
    expect(
      await page.locator('.likes-harness-option [aria-pressed="true"]').getAttribute('data-guide'),
    ).toBe('harness:' + saved.slots[0].loadout.harness);
    const selectedSkills = await page
      .locator('.likes-skill-grid input:checked')
      .evaluateAll((items) =>
        items.map((item) => item.getAttribute('data-guide')!.slice('equip:'.length)),
      );
    expect(selectedSkills.sort()).toEqual([...saved.slots[0].loadout.skills].sort());
    await presets.getByRole('button', { name: 'Save to Preset10', exact: true }).click();
    await expect(presets.getByRole('status')).toHaveText('Custom presets: Preset10 saved.');
    await presets.getByRole('button', { name: 'Overwrite Preset1', exact: true }).click();
    const dialog = page.getByRole('alertdialog', { name: 'Overwrite custom presets', exact: true });
    await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
    expect((await (await api(owner, '/api/games/likes/loadouts')).json()).slots[0].revision).toBe(
      '1',
    );
    await presets.getByRole('button', { name: 'Overwrite Preset1', exact: true }).click();
    await dialog.getByRole('button', { name: 'Confirm overwrite', exact: true }).click();
    await expect(presets.getByRole('status')).toHaveText('Custom presets: Preset1 overwritten.');
    expect((await (await api(owner, '/api/games/likes/loadouts')).json()).slots[0].revision).toBe(
      '2',
    );
    const privateResponse = await api(other, '/api/games/likes/loadouts');
    expect(privateResponse.status()).toBe(200);
    expect(await privateResponse.json()).toEqual({ capacity: 10, slots: [] });
    const state = await (await api(owner, '/api/games/likes/state')).json();
    expect(state.current).toBeFalsy();
    expect(state.queue).toBeFalsy();
    const after = await (await api(owner, '/api/games')).json();
    expect([after.balance, after.game_balance]).toEqual([before.balance, before.game_balance]);
    expect(failures).toEqual([]);
  } finally {
    await owner.close();
    await other.close();
  }
});
async function requestDiagnostics(page: Page, admin: boolean, keyboard = false) {
  const f = fixture();
  const path = admin ? '/logs?' : '/steward?tab=logs&';
  await page.goto((admin ? f.admin_url : f.user_url) + path + 'request_id=' + f.request_ids[0]);
  const dialog = page.getByRole('dialog');
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole('region', { name: 'Request source' })).toContainText(f.source_ip);
  await expect(dialog.getByRole('region', { name: 'Request source' })).toContainText(
    f.source_client,
  );
  await expect(dialog.getByRole('region', { name: 'Request source' })).toContainText(
    'https://audit-source.invalid',
  );
  await expect(dialog.getByRole('region', { name: 'Request source' })).not.toContainText(
    'discard=this',
  );
  await dialog.getByRole('button', { name: 'Upstream error details' }).click();
  const event = dialog
    .locator('details')
    .filter({ has: page.locator('summary', { hasText: 'Error event 1' }) })
    .first();
  await event.locator('summary').first().click();
  const load = event.getByRole('button', { name: 'Load original body' });
  if (keyboard) {
    await load.focus();
    await page.keyboard.press('Enter');
  } else await load.click();
  await expect(event.locator('pre')).toHaveText(f.json_body);
  await event.getByRole('button', { name: 'Format JSON', exact: true }).click();
  await expect(event.locator('pre')).toHaveText(JSON.stringify(JSON.parse(f.json_body), null, 2));
  await event.getByRole('button', { name: 'Original', exact: true }).click();
  await expect(event.locator('pre')).toHaveText(f.json_body);
  await safeSink(page);
}
async function discoveryDiagnostics(page: Page, admin: boolean) {
  const f = fixture();
  await page.goto(admin ? f.admin_url + '/logs' : f.user_url + '/steward?tab=logs');
  const region = page.getByRole('region', { name: 'Discovery and activity diagnostics' });
  await region.getByLabel('Category').selectOption('model_discovery');
  await region.getByLabel('Request, task or operation ID').fill(f.discovery_id);
  await region.getByRole('button', { name: 'Search diagnostics' }).click();
  const entry = region
    .locator('details')
    .filter({ has: page.locator('summary', { hasText: f.discovery_id }) })
    .first();
  await entry.locator('summary').first().click();
  await entry.getByRole('button', { name: 'Load error details and source' }).click();
  await expect(entry.locator('pre')).toHaveText(f.text_body);
  await expect(entry.getByRole('region', { name: 'Request source' })).toContainText(f.source_ip);
  await safeSink(page);
  await region.getByLabel('Category').selectOption('image_task');
  await region.getByLabel('Request, task or operation ID').fill(f.image_task_id);
  await region.getByRole('button', { name: 'Search diagnostics' }).click();
  const task = region
    .locator('details')
    .filter({ has: page.locator('summary', { hasText: f.image_task_id }) })
    .first();
  await task.locator('summary').first().click();
  await task.getByRole('button', { name: 'Load error details and source' }).click();
  await expect(task.locator('pre')).toContainText('"failed"');
}
async function clientRule(page: Page, name: string, editing = false) {
  await page
    .getByRole('group', { name: 'Abuse audit', exact: true })
    .getByRole('button', { name: 'Client rules', exact: true })
    .click();
  if (editing) {
    const card = page
      .getByRole('heading', { name: 'Synthetic client signal', exact: true })
      .locator('..');
    await card.getByRole('button', { name: 'Edit', exact: true }).click();
  } else await page.getByRole('button', { name: 'New rule', exact: true }).click();
  await page.getByLabel('Rule name', { exact: true }).fill(name);
  await page.getByLabel('Match value', { exact: true }).fill(fixture().source_client);
  await page.getByText('Risk classification and supporting evidence', { exact: true }).click();
  await page
    .getByLabel('Evidence note')
    .fill('Synthetic review evidence; a self-reported clue only.');
  await page.getByLabel('Evidence URL', { exact: true }).fill('https://evidence.invalid/review');
  if (editing) await page.getByLabel('Status').selectOption('confirmed');
  const saved = page.waitForResponse(
    (r) =>
      r.url().includes('/abuse-audit/client-rules') &&
      ['POST', 'PATCH'].includes(r.request().method()),
  );
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  expect((await saved).ok()).toBe(true);
  await expect(page.getByRole('heading', { name, exact: true })).toBeVisible();
}
async function riskEvidence(page: Page, sustained = true) {
  const f = fixture();
  const group = page.getByRole('group', { name: 'Abuse audit', exact: true });
  await group.getByRole('button', { name: 'Users', exact: true }).click();
  await page.getByLabel('Risk filter').selectOption('rpm');
  await page.getByRole('button', { name: 'Start new scan', exact: true }).click();
  await expect(page.getByText('Completed', { exact: true })).toBeVisible();
  if (!sustained) {
    await expect(page.getByText('Page 1 of 1 · Total: 0', { exact: true })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'No results yet', exact: true })).toBeVisible();
    await page.getByLabel('Risk filter').selectOption('');
    await page.getByRole('button', { name: 'Start new scan', exact: true }).click();
    await expect(page.getByText('Completed', { exact: true })).toBeVisible();
  }
  const card = page.locator('.card').filter({
    has: page.getByText('User ID: ' + f.users[0].id, { exact: true }),
  });
  await expect(
    card.getByText(sustained ? '5 · Yes' : '0 · No', { exact: true }).first(),
  ).toBeVisible();
  await expect(
    card.locator('dt').filter({ hasText: 'Complete minutes / Incomplete minutes' }).locator('+ dd'),
  ).toHaveText(sustained ? '5 / 0' : '0 / 5');
  await card.getByRole('button', { name: 'Inspect', exact: true }).click();
  await expect(page.getByText('Minute observations', { exact: true })).toBeVisible();
  await group.getByRole('button', { name: 'Shared IPs', exact: true }).click();
  await page.getByRole('button', { name: 'Start new scan', exact: true }).click();
  await expect(page.getByText('Completed', { exact: true })).toBeVisible();
  await expect(page.getByRole('heading', { name: f.source_ip, exact: true })).toBeVisible();
  await expect(page.getByText('Users: 4', { exact: false })).toBeVisible();
  await group.getByRole('button', { name: 'Client matches', exact: true }).click();
  await page.getByRole('button', { name: 'Start new scan', exact: true }).click();
  await expect(page.getByText('Completed', { exact: true })).toBeVisible();
  await expect(page.getByText('Page 1 of 1 · Total: 4', { exact: true })).toBeVisible();
  const savedURL = page.url();
  expect(new URL(savedURL).searchParams.get('audit_scan')).toMatch(/^scn_/);
  await page.reload();
  await expect(page.getByText('Page 1 of 1 · Total: 4', { exact: true })).toBeVisible();
  expect(page.url()).toBe(savedURL);
  const sources = page.locator('summary').filter({ hasText: 'Source information' });
  await sources.first().click();
  await expect(page.getByText(f.source_client, { exact: true }).first()).toBeVisible();
}
function amount(value: string) {
  const magnitude = BigInt(value),
    whole = magnitude / 1000n;
  expect(magnitude % 1000n).toBe(0n);
  return whole.toLocaleString('en-US');
}
test.describe.configure({ mode: 'serial' });

test('an alert opens the exact retained issue context across refresh and mobile layout', async ({
  browser,
}) => {
  const f = fixture();
  const context = await session(browser, 'admin');
  try {
    const page = await context.newPage();
    await page.goto(f.admin_url + '/alerts?alert_id=' + f.projection_alert_id);
    await page.getByRole('button', { name: 'Issue projection', exact: true }).click();
    await expect(
      page.getByRole('heading', { name: 'Retained related issue IDs', exact: true }),
    ).toBeVisible();
    await expect(page.getByText(f.issue_id, { exact: true })).toBeVisible();
    const saved = page.url();
    expect(new URL(saved).searchParams.get('target_kind')).toBe('issue_user');
    expect(new URL(saved).searchParams.get('target_id')).toBe(f.users[0].id);
    await page.setViewportSize({ width: 390, height: 844 });
    await page.reload();
    await expect(page.getByText(f.issue_id, { exact: true })).toBeVisible();
    expect(page.url()).toBe(saved);
    await expect(page.locator('a[href="/users?user=' + f.users[0].id + '"]')).toBeVisible();
    const response = await api(
      context,
      '/admin/api/alerts/targets/issue_user/' + f.users[0].id,
      true,
    );
    expect(response.status()).toBe(200);
    const body = await response.json();
    expect(body.related_issue_ids).toContain(f.issue_id);
    expect(JSON.stringify(body)).not.toContain('synthetic retained issue');
  } finally {
    await context.close();
  }
});

test('administrator reviews raw diagnostics, human audit evidence and all four asset ledgers', async ({
  browser,
}) => {
  const f = fixture();
  const context = await session(browser, 'admin');
  try {
    const page = await context.newPage();
    const timeContext = await api(context, '/admin/api/time-context', true);
    expect(timeContext.status()).toBe(200);
    expect(await timeContext.json()).toEqual({ mode: 'site', offset_minutes: 0 });
    await requestDiagnostics(page, true);
    await discoveryDiagnostics(page, true);
    await page.goto(f.admin_url + '/abuse-audit');
    await clientRule(page, 'Synthetic client signal');
    await riskEvidence(page);
    await page
      .getByRole('group', { name: 'Abuse audit', exact: true })
      .getByRole('button', { name: 'Thresholds', exact: true })
      .click();
    await expect(page.getByLabel('Threshold (%)', { exact: true })).toHaveValue('80');
    await page.getByLabel('Threshold (%)', { exact: true }).fill('75');
    const updated = page.waitForResponse(
      (r) => r.url().endsWith('/abuse-audit/config') && r.request().method() === 'PUT',
    );
    await page.getByRole('button', { name: 'Save', exact: true }).click();
    expect((await updated).status()).toBe(200);
    await expect(page.getByText('Policy revision: 2', { exact: true })).toBeVisible();

    await page.goto(f.admin_url + '/economy-audit');
    await expect(
      page.getByRole('heading', { name: 'Credit economy audit', exact: true }),
    ).toBeVisible();
    const names: Record<Asset, string> = {
      general: 'General credits',
      game: 'Game credits',
      sketch_paper: 'Sketch paper',
      sketch_brush: 'Paint brushes',
    };
    const net: Record<Asset, string> = {
      general: '2800000000',
      game: '4000',
      sketch_paper: '400000',
      sketch_brush: '80000',
    };
    for (const asset of Object.keys(names) as Asset[]) {
      await page.getByLabel('Asset').selectOption(asset);
      await page.getByRole('button', { name: 'Apply', exact: true }).click();
      await expect(page.getByRole('heading', { name: names[asset], exact: true })).toBeVisible();
      const response = await api(context, '/admin/api/economy-audit/summary?asset=' + asset, true);
      expect(response.status()).toBe(200);
      const summary = (await response.json()) as Summary;
      expect(summary.metadata.projected_seq).toBe(summary.metadata.ledger_seq);
      expect(summary.reconciliation.status).toBe('matched');
      expect(summary.reconciliation.inventory_net).toBe(net[asset]);
      expect(summary.reconciliation.ledger_net).toBe(net[asset]);
      expect(summary.inventory).toEqual(f.summaries[asset].inventory);
      const stock = page.getByRole('heading', { name: 'Current stock', exact: true }).locator('..');
      await expect(
        stock
          .locator('dt')
          .filter({ hasText: /^Net stock$/ })
          .locator('..'),
      ).toContainText(amount(net[asset]));
      await expect(
        page.getByText(
          'Retained-ledger reconciliation matches: net stock = issuance − retirement.',
          { exact: true },
        ),
      ).toBeVisible();
      await expect(
        page.getByRole('table', { name: 'Flows by time bucket', exact: true }),
      ).toBeVisible();
      const chart = page.getByRole('slider', { name: 'Issuance and retirement time bucket' });
      await chart.focus();
      await page.keyboard.press('Home');
      await expect(chart).toHaveAttribute('aria-valuenow', '1');
      await expect(page.locator('.audit-chart-selection')).toContainText('New issuance');
      await page.keyboard.press('End');
      await expect(chart).not.toHaveAttribute('aria-valuenow', '1');
      if (asset === 'general') {
        for (const width of [1440, 1920, 2560, 3440, 390]) {
          await page.setViewportSize({ width, height: 1000 });
          await expect
            .poll(() =>
              chart.evaluate((element) => {
                const canvas = element as HTMLCanvasElement;
                const box = canvas.getBoundingClientRect();
                const parent = canvas.parentElement!.getBoundingClientRect();
                return (
                  box.left >= 0 &&
                  box.right <= innerWidth + 1 &&
                  Math.abs(box.width - parent.width) <= 1 &&
                  Math.abs(canvas.width - box.width * devicePixelRatio) <= 1 &&
                  Math.abs(canvas.height - box.height * devicePixelRatio) <= 1
                );
              }),
            )
            .toBe(true);
          await page
            .locator('.audit-chart')
            .screenshot({ path: `test-results/audit/chart-${width}.png` });
        }
        await page.evaluate(() => {
          document.documentElement.dataset.theme = 'dark';
        });
        await page.evaluate(
          () =>
            new Promise<void>((resolve) =>
              requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
            ),
        );
        await page
          .locator('.audit-chart')
          .screenshot({ path: 'test-results/audit/chart-dark.png' });
      }
      if (asset === 'game')
        await expect(
          stock
            .locator('dt')
            .filter({ hasText: /^Negative user balances$/ })
            .locator('..'),
        ).toContainText('1');
    }
    await page
      .getByRole('group', { name: 'Audit view' })
      .getByRole('button', { name: 'Channels', exact: true })
      .click();
    const channels = page.getByRole('table', {
      name: 'Open a channel to inspect its ledger entries',
    });
    await channels
      .getByRole('row')
      .filter({ hasText: 'activity_exchange' })
      .getByRole('button', { name: 'Picture book', exact: true })
      .click();
    const operation = page.locator('details.audit-operation').first();
    await operation.locator('summary').click();
    await expect(operation).toContainText('activity_exchange');
    await expect(operation.getByRole('table')).toContainText('Paint brushes');
    await expect(operation.getByRole('table')).toContainText('General credits');
    await expect(page.getByRole('button', { name: 'Clear operation filter' })).toBeVisible();
  } finally {
    await context.close();
  }
});

test('full steward reads sources and activity diagnostics and edits rules but not thresholds or economy', async ({
  browser,
}) => {
  const f = fixture();
  const context = await session(browser, 6, true);
  try {
    const timeContext = await api(context, '/api/steward/time-context');
    expect(timeContext.status()).toBe(200);
    expect(await timeContext.json()).toEqual({ mode: 'site', offset_minutes: 0 });
    const page = await context.newPage();
    await requestDiagnostics(page, false, true);
    await discoveryDiagnostics(page, false);
    await page.goto(f.user_url + '/steward?tab=risk');
    await clientRule(page, 'Synthetic client reviewed', true);
    // A policy revision must not reinterpret older complete minutes as current evidence.
    await riskEvidence(page, false);
    await page
      .getByRole('group', { name: 'Abuse audit', exact: true })
      .getByRole('button', { name: 'Thresholds', exact: true })
      .click();
    await expect(page.getByLabel('Threshold (%)', { exact: true })).toBeDisabled();
    await expect(page.getByLabel('Threshold (%)', { exact: true })).toHaveValue('75');
    await expect(page.getByRole('button', { name: 'Save', exact: true })).toHaveCount(0);
    await expect(
      page.getByText('Only administrators can change thresholds.', { exact: true }),
    ).toBeVisible();
    const configResponse = await api(context, '/api/steward/abuse-audit/config');
    expect(configResponse.status()).toBe(200);
    const config = (await configResponse.json()) as Record<string, unknown>;
    const denied = await api(context, '/api/steward/abuse-audit/config', false, 'PUT', {
      ...config,
      threshold_percent: 90,
    });
    expect(denied.status()).toBe(403);
    const economy = await api(context, '/admin/api/economy-audit/summary', true);
    expect(economy.status()).toBe(401);
    expect(await economy.text()).not.toContain('"inventory"');
    await page.goto(f.admin_url + '/economy-audit');
    await expect(page.getByLabel('Password', { exact: true })).toBeVisible();
    await expect(
      page.getByRole('heading', { name: 'Credit economy audit', exact: true }),
    ).toHaveCount(0);
  } finally {
    await context.close();
  }
});

for (const level of [5, 1] as const) {
  test(
    level === 5
      ? 'trainee cannot open full-steward audit APIs or deep links'
      : 'ordinary account sees safe own logs without private source or raw error responses',
    async ({ browser }) => {
      const f = fixture();
      const context = await session(browser, level);
      try {
        const timeContext = await api(context, '/api/steward/time-context');
        expect(timeContext.status()).toBe(level === 5 ? 200 : 403);
        if (level === 5)
          expect(await timeContext.json()).toEqual({ mode: 'site', offset_minutes: 0 });
        const page = await context.newPage();
        const noLeaks = safeResponses(page);
        await page.goto(f.user_url + '/steward?tab=risk');
        await expect(page.getByRole('heading', { name: 'Abuse audit', exact: true })).toHaveCount(
          0,
        );
        await expect(page.getByRole('button', { name: 'New rule', exact: true })).toHaveCount(0);
        for (const path of [
          '/api/steward/abuse-audit/users',
          '/api/steward/abuse-audit/client-rules',
          '/api/steward/abuse-audit/client-scans',
          '/api/steward/diagnostics',
          '/api/steward/diagnostics/' + f.diagnostic_id,
          '/api/steward/logs/' + f.request_ids[0] + '/source',
          '/api/steward/logs/' + f.request_ids[0] + '/attempts/1/errors/1',
        ]) {
          const response = await api(context, path);
          expect(response.status()).toBe(403);
          const body = await response.text();
          for (const marker of f.private_markers) expect(body).not.toContain(marker);
        }
        const index = f.users.findIndex((u) => u.level === level);
        const own = await api(context, '/api/logs/' + f.request_ids[index]);
        expect(own.status()).toBe(200);
        const wire = await own.text();
        for (const marker of f.private_markers) expect(wire).not.toContain(marker);
        for (const key of ['effective_ip', 'user_agent', 'bytes_saved', 'source_json'])
          expect(wire).not.toContain('"' + key + '"');
        await page.goto(f.user_url + '/logs?request_id=' + f.request_ids[index]);
        await expect(page.getByRole('dialog')).toBeVisible();
        await expect(page.getByRole('button', { name: 'Upstream error details' })).toHaveCount(0);
        await expect(page.getByRole('region', { name: 'Request source' })).toHaveCount(0);
        await page.goto(f.user_url + '/steward?tab=logs');
        await expect(
          page.getByRole('region', { name: 'Discovery and activity diagnostics' }),
        ).toHaveCount(0);
        const economy = await api(context, '/admin/api/economy-audit/summary', true);
        expect(economy.status()).toBe(401);
        await page.goto(f.admin_url + '/economy-audit');
        await expect(page.getByLabel('Password', { exact: true })).toBeVisible();
        await expect(
          page.getByRole('heading', { name: 'Credit economy audit', exact: true }),
        ).toHaveCount(0);
        await noLeaks();
      } finally {
        await context.close();
      }
    },
  );
}

test('administrator check-in choice persists and both cards follow real daily eligibility', async ({
  browser,
}) => {
  const admin = await session(browser, 'admin');
  const user = await session(browser, 1, true);
  try {
    const settings = await (await api(admin, '/admin/api/site-config', true)).json();
    expect(settings.values.checkin_mutually_exclusive).toBe(false);
    const enabled = await api(admin, '/admin/api/site-config', true, 'PATCH', {
      expected_revision: settings.revision,
      values: {
        checkin_mode: 'enabled',
        game_checkin_mode: 'enabled',
        checkin_award_min_milli: '1',
        checkin_award_max_milli: '1',
        game_checkin_award_min_milli: '2',
        game_checkin_award_max_milli: '2',
        credits_cap_milli: '0',
        game_credits_cap_milli: '0',
      },
    });
    expect(enabled.status()).toBe(200);
    const settingsPage = await admin.newPage();
    await settingsPage.goto(fixture().admin_url + '/settings');
    await settingsPage.getByRole('button', { name: /^Economy/ }).click();
    const choice = settingsPage.locator('#site-setting-checkin_mutually_exclusive');
    await expect(choice).not.toBeChecked();
    await choice.check();
    await settingsPage.getByRole('button', { name: 'Save all changes', exact: true }).click();
    await expect(settingsPage.getByText('Settings saved.', { exact: true })).toBeVisible();
    await settingsPage.reload();
    await settingsPage.getByRole('button', { name: /^Economy/ }).click();
    await expect(choice).toBeChecked();
    const forbidden = await api(
      user,
      '/admin/api/site-config/checkin_mutually_exclusive',
      true,
      'PATCH',
      { value: false },
    );
    expect(forbidden.status()).toBe(401);

    const beforeGeneral = await (await api(user, '/api/checkin')).json();
    const beforeGame = await (await api(user, '/api/checkin/game')).json();
    expect(beforeGeneral.mutually_exclusive).toBe(true);
    expect(beforeGame.mutually_exclusive).toBe(true);
    const page = await user.newPage();
    const failures: string[] = [];
    page.on('pageerror', (error) => failures.push(error.message));
    await page.goto(fixture().user_url + '/');
    await expect(page.locator('.core-checkin-choice-note')).toContainText('Choose one check-in');
    const general = page.locator('.core-checkin-card').filter({
      has: page.getByRole('heading', { name: 'General-credit check-in', exact: true }),
    });
    const game = page.locator('.core-checkin-card').filter({
      has: page.getByRole('heading', { name: 'Game-credit check-in', exact: true }),
    });
    const generalButton = general.getByRole('button', { name: 'Check in', exact: true });
    const gameButton = game.getByRole('button', { name: 'Check in', exact: true });
    await expect(generalButton).toBeEnabled();
    await expect(gameButton).toBeEnabled();
    const generalBox = await general.boundingBox();
    const gameBox = await game.boundingBox();
    expect(generalBox).not.toBeNull();
    expect(gameBox).not.toBeNull();
    expect(Math.abs(generalBox!.y - gameBox!.y)).toBeLessThan(1);
    expect(generalBox!.x + generalBox!.width).toBeLessThan(gameBox!.x);
    await generalButton.click();
    await expect(general).toContainText('Checked in');
    await expect(game).toContainText('Other check-in claimed');
    await expect(generalButton).toBeDisabled();
    await expect(gameButton).toBeDisabled();
    const claimedGeneral = await (await api(user, '/api/checkin')).json();
    const blockedGame = await (await api(user, '/api/checkin/game')).json();
    expect(claimedGeneral.checked_in_today).toBe(true);
    expect(Number(claimedGeneral.balance) - Number(beforeGeneral.balance)).toBe(1);
    expect(blockedGame).toMatchObject({ checked_in_today: false, blocked_by_other_checkin: true });
    expect(blockedGame.balance).toBe(beforeGame.balance);
    const blocked = await api(user, '/api/checkin/game', false, 'POST');
    expect(blocked.status()).toBe(409);
    expect((await blocked.json()).error.code).toBe('already_checked_in');
    await page.reload();
    await expect(gameButton).toBeDisabled();

    await choice.uncheck();
    await settingsPage.getByRole('button', { name: 'Save all changes', exact: true }).click();
    await expect(settingsPage.getByText('Settings saved.', { exact: true })).toBeVisible();
    await page.reload();
    await expect(page.locator('.core-checkin-choice-note')).toHaveCount(0);
    await expect(generalButton).toBeDisabled();
    await expect(gameButton).toBeEnabled();
    await gameButton.click();
    await expect(game).toContainText('Checked in');
    await expect(gameButton).toBeDisabled();
    const claimedGame = await (await api(user, '/api/checkin/game')).json();
    expect(claimedGame.checked_in_today).toBe(true);
    expect(Number(claimedGame.balance) - Number(beforeGame.balance)).toBe(2);
    expect(failures).toEqual([]);
  } finally {
    await admin.close();
    await user.close();
  }
});
