import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import { expect, test } from './test';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { numberedResponse } from './numbered-fixtures';
import commonEn from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import userEn from '../../src/user/i18n/en.json' with { type: 'json' };

const NOW = 1_800_000_000;
const copy = commonEn.common.failurePolicy;
const source = {
  kind: 'custom',
  connector_type: 'ai-sdk-gateway-v3',
  base_url: 'https://gateway.example.test/api',
};
const handling = {
  state: 'pending',
  revision: '1',
  processed_at: null,
  processed_by_role: null,
  closed_at: null,
  closed_reason: null,
};

for (const role of ['owner', 'admin', 'steward'] as const) {
  test(`${role} saves zero independently and retains the warning after reload`, async ({
    page,
  }) => {
    const station = role === 'admin' ? 'admin' : 'user';
    const origin = role === 'admin' ? ADMIN_ORIGIN : USER_ORIGIN;
    const prefix = role === 'admin' ? '/admin/api' : role === 'steward' ? '/api/steward' : '/api';
    const violations = collectConsoleViolations(page);
    await page.setViewportSize({ width: role === 'steward' ? 390 : 1280, height: 900 });
    await page.addInitScript(() => {
      localStorage.setItem('nb.lang', 'en');
      localStorage.setItem('nb.theme', 'dark');
    });
    await mockPublicConfig(page, station);
    await mockRoleSession(
      page,
      station,
      role === 'steward' ? 'level6' : role === 'owner' ? 'user' : 'admin',
    );
    let threshold = '10';
    let revision = '1';
    const writes: unknown[] = [];
    const dialogs: string[] = [];
    page.on('dialog', async (dialog) => {
      dialogs.push(dialog.type());
      await dialog.dismiss();
    });
    const key = () => ({
      id: '11',
      endpoint_key_id: '21',
      display_head: 'fixture',
      display_tail: 'key',
      safe_source: source,
      physical_enabled: false,
      charity_state: 'disabled',
      limits: { price: null, calls: null, tokens: null },
      usage: {
        price_used: '0',
        price_inflight: '0',
        calls_used: '0',
        calls_inflight: '0',
        tokens_used: '0',
        tokens_inflight: '0',
      },
      token_reserve: 0,
      expires_at: null,
      failure_disable_threshold: threshold,
      streak: { generation: '4', count: '12', failure_disabled: threshold !== '0' },
      ended_reason: null,
      ...(role === 'owner'
        ? {}
        : {
            binding_count: '0',
            idle: true,
            authorized_expires_at: null,
            safe_note: '',
            max_concurrency: 0,
            max_rpm: 0,
          }),
    });
    const donation = () => ({
      id: '7',
      status: 'approved',
      revision,
      description: 'Gateway policy fixture',
      review_result: { decision: 'approve', reason: '', reviewed_at: NOW },
      keys: [key()],
      created_at: NOW,
      updated_at: NOW,
      ...(role === 'owner'
        ? {}
        : {
            handling,
            owner: { user_id: '42', discord_id: null, display_name: 'Synthetic donor' },
            reviewer: null,
          }),
    });
    const summary = () => {
      const { keys: _keys, ...rest } = donation();
      void _keys;
      return {
        ...rest,
        key_count: '1',
        source_count: '1',
        sources: [source],
        state_counts: {
          available: '0',
          pending: '0',
          disabled: '1',
          suspended: '0',
          exhausted: '0',
          expired: '0',
          ended: '0',
        },
      };
    };
    await page.route('**/*', async (route) => {
      const request = route.request();
      const url = new URL(request.url());
      if (url.origin !== origin) return route.fallback();
      if (url.pathname === '/api/charity/models')
        return route.fulfill({
          json: {
            state: 'no_models',
            models: [],
            donation_intake: 'closed',
            server_now: NOW,
            ...(url.searchParams.has('page')
              ? { pagination: { page: '1', page_size: 20, total_items: '0', total_pages: '1' } }
              : {}),
          },
        });
      if (!url.pathname.startsWith(prefix + '/donations')) return route.fallback();
      if (request.method() === 'PATCH') {
        expect(url.pathname).toBe(prefix + '/donations/7/keys/11/failure-policy');
        expect(request.headers()['idempotency-key']).toMatch(/^[A-Za-z0-9_-]{22,128}$/);
        const body = request.postDataJSON() as {
          expected_revision: string;
          failure_disable_threshold: string;
        };
        expect(body).toEqual({ expected_revision: revision, failure_disable_threshold: '0' });
        writes.push(body);
        threshold = '0';
        revision = '2';
        return route.fulfill({
          json: {
            donation_id: '7',
            donation_key_id: '11',
            failure_disable_threshold: threshold,
            failure_streak: '12',
            failure_disabled: false,
            revision,
          },
        });
      }
      expect(request.method()).toBe('GET');
      if (url.pathname.endsWith('/badge'))
        return route.fulfill({ json: { pending_count: '1', server_now: NOW } });
      if (url.pathname.endsWith('/7')) return route.fulfill({ json: donation() });
      if (url.pathname.endsWith('/7/keys'))
        return route.fulfill({
          json: numberedResponse(
            [
              {
                ...key(),
                donation_id: '7',
                key_id: '11',
                donation_revision: revision,
                rule_count: '0',
                rules: [],
                ...(role === 'owner' ? {} : { handling }),
              },
            ],
            '1',
            20,
          ),
        });
      return route.fulfill({ json: numberedResponse([summary()], '1', 20) });
    });
    await page.goto(
      origin +
        (role === 'owner'
          ? '/charity?tab=donations'
          : role === 'admin'
            ? '/charity'
            : '/steward?tab=charity'),
    );
    await expect(page.getByText('Gateway policy fixture', { exact: true })).toBeVisible();
    await page
      .getByRole('button', {
        name: role === 'owner' ? userEn.user.charity.ownerPages.keys : 'Review',
        exact: true,
      })
      .click();
    const control = page.locator('.failure-policy-control').first();
    await expect(control).toBeVisible();
    await control.getByLabel(copy.label).fill('0');
    await expect(control.locator('.failure-policy-warning')).toBeVisible();
    await control.getByRole('button', { name: copy.save, exact: true }).click();
    await expect.poll(() => writes.length).toBe(1);
    await expect(
      control.getByText(copy.saved.replace('{{value}}', '0'), { exact: true }),
    ).toBeVisible();
    expect(dialogs).toEqual([]);
    await page.reload();
    await expect(control).toBeVisible();
    await expect(control.getByLabel(copy.label)).toHaveValue('0');
    await expect(control.locator('.failure-policy-warning').first()).toBeVisible();
    expect(await control.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(
      true,
    );
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    if (process.env.NONBIRI_VISUAL_DIR) {
      await mkdir(process.env.NONBIRI_VISUAL_DIR, { recursive: true });
      await control.screenshot({
        path: resolve(process.env.NONBIRI_VISUAL_DIR, `failure-policy-${role}.png`),
      });
    }
    violations.assertNone();
  });
}

test('Gateway attribution defaults off and uses the normal administrator save flow', async ({
  page,
}) => {
  await page.addInitScript(() => localStorage.setItem('nb.lang', 'en'));
  await mockPublicConfig(page, 'admin');
  await mockRoleSession(page, 'admin', 'admin');
  const key = 'gateway_user_attribution_enabled';
  let enabled = false;
  let revision = '1';
  const patches: unknown[] = [];
  const label = 'Send Gateway cost attribution';
  await page.route('**/*', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.origin !== ADMIN_ORIGIN) return route.fallback();
    if (url.pathname === '/admin/api/site-config/catalog')
      return route.fulfill({
        json: {
          data: [
            {
              key,
              group: 'connector',
              type: 'boolean',
              title: { zh: '发送 Gateway 费用归因标签', en: label },
              description: {
                zh: '默认不发送',
                en: 'Off by default. A gateway can link requests for cost attribution.',
              },
              unit: { zh: '无', en: 'none' },
              nullable: false,
              null_writable: false,
              raw_default: false,
              effective_fallback: false,
              minimum: null,
              maximum: null,
              step: null,
              allowed_values: [],
              zero_semantics: { zh: '不发送', en: 'No tag sent' },
              null_semantics: { zh: '不可为空', en: 'Not nullable' },
              empty_semantics: { zh: '不可为空字符串', en: 'No empty string' },
              independent_gates: [],
              write_endpoint: '/admin/api/site-config/' + key,
            },
          ],
        },
      });
    if (url.pathname !== '/admin/api/site-config') return route.fallback();
    if (request.method() === 'GET')
      return route.fulfill({ json: { revision, values: { [key]: enabled } } });
    expect(request.method()).toBe('PATCH');
    const body = request.postDataJSON() as {
      expected_revision: string;
      values: Record<string, boolean>;
    };
    expect(body).toEqual({ expected_revision: revision, values: { [key]: !enabled } });
    patches.push(body);
    enabled = !enabled;
    revision = String(Number(revision) + 1);
    return route.fulfill({ json: { revision, changed_keys: [key] } });
  });
  await page.goto(ADMIN_ORIGIN + '/settings');
  const input = page.getByLabel(label, { exact: true });
  if (!(await input.isVisible())) await page.getByText('Connectors', { exact: true }).click();
  await expect(input).toHaveValue('false');
  for (const value of ['true', 'false']) {
    await input.selectOption(value);
    await page.getByRole('button', { name: 'Save all changes' }).click();
    await expect.poll(() => enabled).toBe(value === 'true');
    await page.reload();
    if (!(await input.isVisible())) await page.getByText('Connectors', { exact: true }).click();
    await expect(input).toHaveValue(value);
  }
  expect(patches).toHaveLength(2);
});
