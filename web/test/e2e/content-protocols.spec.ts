import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import { numberedPage } from './numbered-fixtures';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { expect, test } from './test';
import {
  createOptions,
  mockConnectionResources,
  mixedMainstreamLayout,
  mainstreamProjection,
} from './connection-fixtures';
import common from '../../src/shared/i18n/common/en.json' with { type: 'json' };

type Page = Parameters<typeof collectConsoleViolations>[0];

const protocols = [
  ['google-gemini', 'Google Gemini (Chat)'],
  ['openai-compatible', 'OpenAI-compatible'],
  ['typesafe-systemone', 'Typesafe System One'],
  ['cohere-rerank', 'Cohere Rerank'],
  ['jina-rerank', 'Jina Rerank'],
  ['siliconflow-rerank', 'SiliconFlow Rerank'],
  ['voyage-rerank', 'Voyage AI Rerank'],
] as const;
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
      const c = common.common.connections;
      await mockConnectionResources(page, () => endpoints);
      await page.route(`${USER_ORIGIN}/api/**`, async (route) => {
        const request = route.request();
        const url = new URL(request.url());
        if (url.pathname === '/api/endpoint-create-options') {
          await route.fulfill({
            json: createOptions,
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
        await page.goto(`${USER_ORIGIN}/endpoints?connection_id=1`);
        await page.getByRole('button', { name: c.addEntry, exact: true }).first().click();
        const wizard = page
          .locator('.nb-expandable-panel')
          .filter({ has: page.getByRole('heading', { name: c.addEntry, exact: true }) });
        const choice = wizard.getByRole('combobox', { name: c.protocol, exact: true });
        await choice.selectOption(kind);
        await expect(choice).toHaveValue(kind);
        await expect(choice.locator('option[value="openai-responses"]')).toHaveCount(0);
        await expect(choice.locator('option[value="openai-chat-completions"]')).toHaveCount(0);
        if (kind === 'google-gemini') await screenshot(page, `protocol-user-${width}-${theme}`);
        if (kind === 'openai-compatible')
          await wizard
            .getByRole('checkbox', { name: common.common.modelTypes.decisions, exact: true })
            .check();
        await wizard.getByLabel(c.entryURL, { exact: true }).fill(baseURL);
        await wizard.getByLabel(c.note + ' ' + c.optional, { exact: true }).fill(label);
        await wizard.getByRole('button', { name: c.saveEntries, exact: true }).click();
        await expect(wizard.getByText(c.entrySaved)).toBeVisible();
        expect(submitted).toMatchObject({
          source: 'custom',
          connector_type: kind,
          base_url: baseURL,
          note: label,
          native_services:
            kind === 'openai-compatible'
              ? ['chat_completions', 'responses', 'decisions']
              : createOptions.native_service_options.find((row) => row.connector_type === kind)!
                  .default_native_services,
        });
      }

      await mockPublicConfig(page, 'admin');
      await mockRoleSession(page, 'admin', 'admin');
      const channels: Array<Record<string, unknown>> = [];
      await page.route(`${ADMIN_ORIGIN}/admin/api/endpoint-create-options`, (route) =>
        route.fulfill({ json: createOptions }),
      );
      await page.route(`${ADMIN_ORIGIN}/admin/api/mainstream-channels**`, async (route) => {
        const request = route.request();
        const url = new URL(request.url());
        if (request.method() === 'POST') {
          submitted = request.postDataJSON();
          if (mixedMainstreamLayout(submitted!))
            return route.fulfill({
              status: 400,
              json: { error: { code: 'invalid_request', message: 'Use one channel layout.' } },
            });
          const channel = {
            ...submitted,
            ...mainstreamProjection(submitted!),
            entries: (submitted!.entries as Record<string, unknown>[]).map((entry, index) => ({
              ...entry,
              id: String(index + 1),
            })),
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
        await page.getByRole('button', { name: c.addEntry, exact: true }).click();
        const options = page.getByRole('combobox', { name: c.protocol, exact: true });
        await options.selectOption(kind);
        await expect(options).toHaveValue(kind);
        if (kind === 'openai-compatible')
          await page
            .getByRole('checkbox', { name: common.common.modelTypes.decisions, exact: true })
            .check();
        await page.getByLabel(c.entryURL, { exact: true }).fill(baseURL);
        if (kind === 'google-gemini') await screenshot(page, `protocol-admin-${width}-${theme}`);
        await page.getByRole('button', { name: 'Create channel', exact: true }).click();
        await expect(
          page.getByRole('heading', { name: 'Channel details', exact: true }),
        ).toBeVisible();
        expect(submitted).toMatchObject({
          name: label,
          entries: [
            expect.objectContaining({
              connector_type: kind,
              native_services:
                kind === 'openai-compatible'
                  ? ['chat_completions', 'responses', 'decisions']
                  : createOptions.native_service_options.find((row) => row.connector_type === kind)!
                      .default_native_services,
            }),
          ],
        });
        expect(submitted).not.toHaveProperty('connector_type');
        expect(submitted).not.toHaveProperty('base_url');
      }
      guard.assertNone();
    });
  }
}
