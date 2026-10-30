import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import { numberedPage } from './numbered-fixtures';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { expect, test } from './test';
import {
  createOptions,
  credential,
  mockConnectionResources,
  mixedMainstreamLayout,
  mainstreamProjection,
} from './connection-fixtures';
import common from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import userCopy from '../../src/user/i18n/en.json' with { type: 'json' };
import { serviceCopy } from '../service-onboarding';

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
      const grants: Array<Record<string, unknown>> = [];
      const sources: Array<Record<string, unknown>> = [];
      const writes: { path: string; body: Record<string, unknown>; identity?: string }[] = [];
      let submitted: Record<string, unknown> | null = null;
      const c = common.common.connections;
      const workflow = serviceCopy();
      const serviceUI = userCopy.user.services;
      await mockConnectionResources(page, () => endpoints);
      await page.route(`${USER_ORIGIN}/api/**`, async (route) => {
        const request = route.request();
        const url = new URL(request.url());
        const input = request.method() === 'POST' ? request.postDataJSON() : null;
        if (input || (request.method() === 'POST' && url.pathname.endsWith('/models/refresh')))
          writes.push({
            path: url.pathname,
            body: input ?? {},
            identity: request.headers()['idempotency-key'],
          });
        if (url.pathname === '/api/endpoint-create-options') {
          await route.fulfill({
            json: createOptions,
          });
        } else if (url.pathname === '/api/endpoints' && request.method() === 'POST') {
          submitted = input;
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
        } else if (/^\/api\/endpoints\/\d+\/keys$/.test(url.pathname) && input) {
          const endpointID = url.pathname.split('/')[3];
          const automatic = input.discovery_source_id !== null;
          const sourceId = automatic ? String(51 + sources.length) : null;
          if (automatic) sources.push({ id: sourceId, endpoint_id: endpointID });
          const grant = {
            ...input,
            discovery_source_id: sourceId,
            id: String(21 + grants.length),
            endpoint_id: endpointID,
            credential_version: credential.credential_version,
            grant_revision: '1',
            display_head: credential.display_head,
            display_tail: credential.display_tail,
            suspension_state: 'none',
            revision: '1',
            created_at: 1_800_000_000,
            updated_at: 1_800_000_000,
          };
          grants.push(grant);
          await route.fulfill({ status: 201, json: grant });
        } else if (/^\/api\/endpoints\/\d+$/.test(url.pathname)) {
          await route.fulfill({
            json: endpoints.find((row) => row.id === url.pathname.split('/')[3]),
          });
        } else if (/^\/api\/endpoints\/\d+\/keys$/.test(url.pathname)) {
          await route.fulfill({
            json: numberedPage(
              grants.filter((row) => row.endpoint_id === url.pathname.split('/')[3]),
              url.searchParams,
            ),
          });
        } else if (/\/models\/refresh$/.test(url.pathname)) {
          await route.fulfill({
            status: 202,
            json: {
              operation_id: 'op_000000000000000000000A',
              evidence: {
                state: 'checking',
                revision: '1',
                result: null,
                safe_class: 'none',
                observed_at: 1800000000,
                count: null,
              },
            },
          });
        } else if (/^\/api\/endpoints\/\d+\/keys\/\d+\/models$/.test(url.pathname)) {
          const window = url.searchParams.has('limit') ? null : numberedPage([], url.searchParams);
          await route.fulfill({
            json: {
              evidence: {
                state: 'succeeded',
                revision: '2',
                result: 'empty',
                safe_class: 'none',
                observed_at: 1800000000,
                count: '0',
              },
              automatic_entries: [],
              manual_entries: [],
              next_cursor: null,
              ...(window ? { pagination: window.pagination } : {}),
            },
          });
        } else if (/^\/api\/discovery-sources\/\d+\/models$/.test(url.pathname)) {
          await route.fulfill({ json: numberedPage([], url.searchParams) });
        } else if (url.pathname === '/api/credentials/31') {
          await route.fulfill({ json: credential });
        } else if (url.pathname === '/api/connections/1/discovery-sources') {
          if (input) {
            const source = {
              ...input,
              id: String(51 + sources.length),
              name: '',
              connection_id: '1',
              credential_version: credential.credential_version,
              models_url: baseURL + '/models',
              revision: '1',
              discovery_source_revision: '1',
              state: 'unknown',
              checked_at: null,
              safe_class: 'none',
              safe_diag: '',
              created_at: 1_800_000_000,
              updated_at: 1_800_000_000,
            };
            sources.push(source);
            await route.fulfill({ status: 201, json: source });
          } else {
            await route.fulfill({ json: numberedPage(sources, url.searchParams) });
          }
        } else if (url.pathname === '/api/endpoints') {
          await route.fulfill({ json: numberedPage(endpoints, url.searchParams) });
        } else {
          await route.fallback();
        }
      });
      for (const [kind, label] of protocols) {
        const supportsDiscovery = [
          'google-gemini',
          'openai-compatible',
          'typesafe-systemone',
        ].includes(kind);
        await page.goto(`${USER_ORIGIN}/endpoints?connection_id=1`);
        await page.getByRole('button', { name: c.addEntry, exact: true }).first().click();
        const wizard = page
          .locator('.nb-expandable-panel')
          .filter({ has: page.getByRole('heading', { name: c.addEntry, exact: true }) });
        await wizard.locator('summary').filter({ hasText: serviceUI.advancedSetup }).click();
        const choice = wizard.getByRole('combobox', { name: c.protocol, exact: true });
        await choice.selectOption(kind);
        await expect(choice).toHaveValue(kind);
        await expect(choice.locator('option[value="openai-responses"]')).toHaveCount(0);
        await expect(choice.locator('option[value="openai-chat-completions"]')).toHaveCount(0);
        if (kind === 'google-gemini') await screenshot(page, `protocol-user-${width}-${theme}`);
        await wizard.getByLabel(serviceUI.serviceAddress, { exact: true }).fill(baseURL);
        await wizard.getByLabel(workflow.serviceName, { exact: true }).fill(label);
        await expect(
          wizard.getByRole('radio', { name: c.existingCredential, exact: true }),
        ).toBeChecked();
        await expect(wizard.getByLabel(c.secret, { exact: true })).toHaveCount(0);
        const save = wizard.getByRole('button', { name: serviceUI.addAndFetch, exact: true });
        await expect(save).toBeDisabled();
        await wizard
          .getByRole('table', { name: c.chooseCredential, exact: true })
          .getByRole('button', { name: c.select, exact: true })
          .click();
        await expect(wizard.getByRole('checkbox', { name: c.ownership, exact: true })).toHaveCount(
          0,
        );
        await expect(save).toBeEnabled();
        await save.click();
        await expect(wizard.getByText(workflow.serviceReady, { exact: true })).toBeVisible();
        if (supportsDiscovery)
          await expect(wizard.getByText(c.saved, { exact: true })).toBeVisible();
        const nativeServices = createOptions.native_service_options.find(
          (row) => row.connector_type === kind,
        )!.native_services;
        expect(submitted).toEqual({
          source: 'custom',
          connection_id: '1',
          connector_type: kind,
          base_url: baseURL,
          note: label,
          native_services: nativeServices,
          rerank_format: '',
          enabled: true,
        });
        const grantWrite = writes
          .filter((write) => write.path === `/api/endpoints/${endpoints.length}/keys`)
          .at(-1)!;
        expect(grantWrite.body).toEqual({
          credential_id: credential.id,
          native_services: nativeServices,
          ...(supportsDiscovery ? {} : { discovery_source_id: null }),
          note: '',
          enabled: true,
          ownership_confirmed: true,
          force_store_false: false,
          max_concurrency: 0,
          max_rpm: 0,
        });
      }
      expect(writes.filter((write) => write.path === '/api/endpoints')).toHaveLength(
        protocols.length,
      );
      expect(writes.filter((write) => /\/keys$/.test(write.path))).toHaveLength(protocols.length);
      expect(
        writes.filter((write) => write.path === '/api/connections/1/discovery-sources'),
      ).toHaveLength(0);
      expect(writes.filter((write) => write.path.endsWith('/models/refresh'))).toHaveLength(3);
      expect(writes.filter((write) => !write.identity).map((write) => write.path)).toEqual([]);

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
              native_services: createOptions.native_service_options.find(
                (row) => row.connector_type === kind,
              )!.native_services,
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
