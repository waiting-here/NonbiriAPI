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
  if (!sustained) {
    await expect(page.getByText('No entries on this page', { exact: true })).toBeVisible();
    await page.getByLabel('Risk filter').selectOption('');
  }
  const row = page
    .getByRole('row')
    .filter({ has: page.getByRole('cell', { name: f.users[0].id, exact: true }) });
  await expect(row).toContainText(sustained ? '5 · Yes' : '0 · No');
  if (!sustained) await expect(row.getByRole('cell').nth(6)).toHaveText('5');
  await row.getByRole('button', { name: 'Inspect', exact: true }).click();
  await expect(page.getByText('Minute observations', { exact: true })).toBeVisible();
  await group.getByRole('button', { name: 'Shared IPs', exact: true }).click();
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

test('administrator reviews raw diagnostics, human audit evidence and all four asset ledgers', async ({
  browser,
}) => {
  const f = fixture();
  const context = await session(browser, 'admin');
  try {
    const page = await context.newPage();
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
