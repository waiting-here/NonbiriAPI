import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import { numberedPage } from './numbered-fixtures';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { expect, test } from './test';

type Page = Parameters<typeof collectConsoleViolations>[0];

const protocols = [
  ['google-gemini', 'Google Gemini (Chat)'],
  ['openai-responses', 'OpenAI Responses (Chat)'],
  ['openai-decisions', 'OpenAI Decisions'],
] as const;
const allTypes = [
  'openai-compatible',
  'anthropic-compatible',
  'ai-sdk-gateway-v3',
  ...protocols.map(([kind]) => kind),
];
const baseURL = 'https://shared.example.test/v1';

async function screenshot(page: Page, name: string) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  if (process.env.NONBIRI_VISUAL_DIR) {
    const directory = resolve(process.env.NONBIRI_VISUAL_DIR);
    await mkdir(directory, { recursive: true });
    await page.screenshot({ path: resolve(directory, `${name}.png`), fullPage: true });
    await page.screenshot({ path: resolve(directory, `${name}-viewport.png`) });
  }
}

for (const width of [1440, 390]) {
  for (const theme of ['light', 'dark']) {
    test(`native protocol choices and creation at ${width}px ${theme}`, async ({ page }) => {
      const guard = collectConsoleViolations(page);
      await page.setViewportSize({ width, height: 900 });
      await page.addInitScript((theme) => {
        localStorage.setItem('nb.lang', 'en');
        localStorage.setItem('nb.theme', theme);
      }, theme);
      await mockPublicConfig(page, 'user');
      await mockRoleSession(page, 'user', 'user');
      const endpoints: Array<Record<string, unknown>> = [];
      let submitted: Record<string, unknown> | null = null;
      await page.route(`${USER_ORIGIN}/api/**`, async (route) => {
        const request = route.request();
        const url = new URL(request.url());
        if (url.pathname === '/api/endpoint-create-options') {
          await route.fulfill({
            json: { base_connector_types: allTypes, mainstream_channels: [] },
          });
        } else if (url.pathname === '/api/endpoints' && request.method() === 'POST') {
          submitted = request.postDataJSON();
          const endpoint = {
            ...submitted,
            id: String(endpoints.length + 1),
            origin: { kind: 'custom' },
            revision: '1',
            key_count: '0',
            created_at: 1_800_000_000,
            updated_at: 1_800_000_000,
          };
          endpoints.push(endpoint);
          await route.fulfill({ status: 201, json: endpoint });
        } else if (url.pathname === '/api/endpoints') {
          await route.fulfill({ json: numberedPage(endpoints, url.searchParams) });
        } else {
          await route.fallback();
        }
      });
      for (const [kind, label] of protocols) {
        await page.goto(`${USER_ORIGIN}/endpoints`);
        await page.getByRole('button', { name: '＋ Add service', exact: true }).first().click();
        const quickProtocol = page.getByRole('combobox', { name: 'API format', exact: true });
        await quickProtocol.selectOption(kind);
        await expect(quickProtocol).toHaveValue(kind);
        if (kind === 'google-gemini') await screenshot(page, `protocol-quick-${width}-${theme}`);
        await page
          .getByRole('button', { name: 'Need advanced options? Set up manually', exact: true })
          .click();
        const wizard = page.getByRole('region', { name: 'Add service manually', exact: true });
        const choice = wizard.getByRole('button', { name: label, exact: true });
        await choice.focus();
        await page.keyboard.press('Enter');
        await expect(choice).toHaveAttribute('aria-pressed', 'true');
        if (kind === 'google-gemini') await screenshot(page, `protocol-user-${width}-${theme}`);
        await wizard.getByRole('button', { name: 'Next', exact: true }).click();
        await wizard.getByLabel('Service address').fill(baseURL);
        await wizard.getByLabel('Note', { exact: true }).fill(label);
        await wizard.getByRole('button', { name: 'Create service', exact: true }).click();
        await expect(page.getByLabel('Key', { exact: true })).toBeVisible();
        expect(submitted).toMatchObject({
          source: 'custom',
          connector_type: kind,
          base_url: baseURL,
          note: label,
        });
      }

      await mockPublicConfig(page, 'admin');
      await mockRoleSession(page, 'admin', 'admin');
      const channels: Array<Record<string, unknown>> = [];
      await page.route(`${ADMIN_ORIGIN}/admin/api/mainstream-channels**`, async (route) => {
        const request = route.request();
        const url = new URL(request.url());
        if (request.method() === 'POST') {
          submitted = request.postDataJSON();
          const channel = {
            ...submitted,
            id: `mch_${String.fromCharCode(65 + channels.length).repeat(21)}A`,
            state: 'active',
            revision: '1',
            created_at: 1_800_000_000,
            updated_at: 1_800_000_000,
            retired_at: null,
          };
          channels.push(channel);
          await route.fulfill({ status: 201, json: channel });
        } else if (url.pathname === '/admin/api/mainstream-channels') {
          await route.fulfill({ json: numberedPage(channels, url.searchParams) });
        } else {
          await route.fulfill({
            json: channels.find((channel) => url.pathname.endsWith(`/${channel.id}`)),
          });
        }
      });
      for (const [kind, label] of protocols) {
        await page.goto(`${ADMIN_ORIGIN}/mainstream-channels`);
        await page.getByRole('button', { name: /New channel/ }).click();
        await page.getByLabel('Channel name').fill(label);
        const options = page.getByRole('combobox', { name: 'API format', exact: true });
        await options.selectOption(kind);
        await expect(options).toHaveValue(kind);
        await page.getByRole('textbox', { name: /^API base URL/ }).fill(baseURL);
        if (kind === 'google-gemini') await screenshot(page, `protocol-admin-${width}-${theme}`);
        await page.getByRole('button', { name: 'Create channel', exact: true }).click();
        await expect(
          page.getByRole('heading', { name: 'Channel details', exact: true }),
        ).toBeVisible();
        expect(submitted).toMatchObject({ connector_type: kind, base_url: baseURL, name: label });
      }
      guard.assertNone();
    });
  }
}
