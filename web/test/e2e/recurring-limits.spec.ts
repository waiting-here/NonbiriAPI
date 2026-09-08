import { mkdir, writeFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import type {
  RecurringLimitRuleView,
  RecurringLimitsWritePayload,
} from '../../src/shared/operations/recurringLimits';
import { expect, test, type Page } from './test';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import {
  assertNoSensitiveBrowserPersistence,
  installURLPersistenceObserver,
  mockPublicConfig,
  mockRoleSession,
} from './support';

type BrowserContext = Parameters<typeof installURLPersistenceObserver>[0];
type Route = Parameters<Parameters<Page['route']>[1]>[0];

const NOW = 1_800_000_000;
const RULE_ID = `qlr_${'A'.repeat(21)}Q`;
const MARKER = 'quota-draft-ephemeral-7e49a2c1';

test.afterEach(async ({ page }, info) => {
  if (info.status === info.expectedStatus || !process.env.NONBIRI_VISUAL_DIR) return;
  const dir = resolve(process.env.NONBIRI_VISUAL_DIR);
  await mkdir(dir, { recursive: true });
  await writeFile(
    resolve(dir, 'quota-failure-page.txt'),
    await page.locator('body').ariaSnapshot(),
  );
  await page.screenshot({ path: resolve(dir, 'quota-failure-page.png'), fullPage: true });
});

const KEY = {
  id: '11',
  endpoint_key_id: '21',
  display_head: 'safe-head',
  display_tail: 'safe-tail',
  safe_source: {
    kind: 'custom',
    connector_type: 'openai-compatible',
    base_url: 'https://donor.example.test/v1',
  },
  physical_enabled: true,
  charity_state: 'available',
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
  streak: { generation: '1', count: '0', failure_disabled: false },
  ended_reason: null,
};

function rule(overrides: Partial<RecurringLimitRuleView> = {}): RecurringLimitRuleView {
  return {
    id: RULE_ID,
    mode: 'reset',
    interval: '5h',
    alignment: 'first_success',
    time_zone: 'UTC',
    week_starts_on: null,
    metric: 'calls',
    limit: '100',
    used: '90',
    reserved: '5',
    remaining: '5',
    state: 'available',
    period_start: NOW,
    period_end: NOW + 18_000,
    next_transition_at: NOW + 18_000,
    ...overrides,
  };
}

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({
    status,
    headers: { 'content-type': 'application/json', 'cache-control': 'no-store' },
    body: JSON.stringify(body),
  });
}

async function prepare(
  context: BrowserContext,
  page: Page,
  role: 'admin' | 'steward' | 'owner',
  locale: 'en' | 'zh',
  theme: 'light' | 'dark',
  width: number,
) {
  const station = role === 'admin' ? 'admin' : 'user';
  const origin = role === 'admin' ? ADMIN_ORIGIN : USER_ORIGIN;
  const base = role === 'admin' ? '/admin/api' : role === 'steward' ? '/api/steward' : '/api';
  const path = `${base}/donations/7/keys/11/recurring-limits`;
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  page.on('console', (message) => {
    if (message.type() !== 'error' && message.type() !== 'warning') return;
    if (message.location().url === `${origin}${path}` && /status of 409/.test(message.text()))
      return;
    errors.push(message.text());
  });
  await installURLPersistenceObserver(context, [MARKER]);
  await page.setViewportSize({ width, height: 900 });
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.addInitScript(
    ({ locale, theme }) => {
      localStorage.setItem('nb.lang', locale);
      localStorage.setItem('nb.theme', theme);
    },
    { locale, theme },
  );
  await mockPublicConfig(page, station);
  await mockRoleSession(
    page,
    station,
    role === 'steward' ? 'level5' : role === 'owner' ? 'user' : 'admin',
  );
  const state = {
    revision: '7',
    rules: [rule()],
    reads: 0,
    writes: [] as Array<{ payload: RecurringLimitsWritePayload; key: string }>,
    firstReadInvalid: false,
    firstWriteUnknown: false,
    firstWriteConflict: false,
    releaseSave: null as null | (() => void),
    pauseSave: false,
  };
  const receipts = new Map<
    string,
    { donation_id: string; key_id: string; donation_revision: string }
  >();
  const donation = () => {
    const common = {
      id: '7',
      status: 'approved',
      revision: state.revision,
      description: 'Synthetic recurring quota donation',
      review_result: { decision: 'approve', reason: 'Reviewed', reviewed_at: NOW },
      created_at: NOW - 60,
      updated_at: NOW,
    };
    return role === 'owner'
      ? { ...common, keys: [KEY] }
      : {
          ...common,
          keys: [
            { ...KEY, binding_count: '0', idle: true, authorized_expires_at: null, safe_note: '' },
          ],
          owner:
            role === 'steward'
              ? null
              : { user_id: '42', discord_id: null, display_name: 'Synthetic donor' },
          reviewer: { user_id: null, role: 'admin' },
          handling: {
            state: 'pending',
            revision: '1',
            processed_at: null,
            processed_by_role: null,
            closed_at: null,
            closed_reason: null,
          },
        };
  };
  await page.route('**/*', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.origin !== origin) return route.fallback();
    if (request.method() === 'GET' && url.pathname === `${base}/donations`) {
      return json(route, { data: [donation()], next_cursor: null });
    }
    if (request.method() === 'GET' && url.pathname === `${base}/donations/7`) {
      return json(route, donation());
    }
    if (url.pathname !== path) return route.fallback();
    if (request.method() === 'GET') {
      state.reads++;
      if (state.firstReadInvalid && state.reads === 1) return json(route, { invalid: true });
      return json(route, {
        donation_id: '7',
        key_id: '11',
        donation_revision: state.revision,
        server_now: NOW,
        rules: state.rules,
      });
    }
    if (request.method() === 'PUT') {
      const payload = request.postDataJSON() as RecurringLimitsWritePayload;
      const key = request.headers()['idempotency-key'] ?? '';
      state.writes.push({ payload, key });
      const prior = receipts.get(key);
      if (prior) return json(route, prior);
      if (state.firstWriteConflict && state.writes.length === 1) {
        state.revision = '8';
        state.rules = [
          rule({
            time_zone: 'Asia/Tokyo',
            interval: 'week',
            alignment: 'calendar',
            week_starts_on: 7,
            period_start: Date.UTC(2027, 0, 9, 15) / 1000,
            period_end: Date.UTC(2027, 0, 16, 15) / 1000,
            next_transition_at: Date.UTC(2027, 0, 16, 15) / 1000,
          }),
        ];
        return json(
          route,
          {
            error: { code: 'conflict', source: 'platform', message: 'Synthetic concurrent edit.' },
          },
          409,
        );
      }
      if (state.pauseSave) {
        await new Promise<void>((done) => {
          state.releaseSave = done;
        });
      }
      state.revision = String(BigInt(state.revision) + 1n);
      const previousRules = state.rules;
      state.rules = payload.rules.map((input, index) => {
        const previous = previousRules.find((entry) => entry.id === input.id);
        const structural =
          !previous ||
          (
            ['mode', 'interval', 'alignment', 'time_zone', 'week_starts_on', 'metric'] as const
          ).some((field) => previous[field] !== input[field]);
        const retained = structural ? 0n : 95n;
        const reset = structural
          ? {
              used: '0',
              reserved: '0',
              state:
                input.alignment === 'first_success'
                  ? ('waiting_first_success' as const)
                  : ('available' as const),
              period_start:
                input.alignment === 'calendar' ? Date.UTC(2027, 0, 12, 18, 30) / 1000 : null,
              period_end:
                input.alignment === 'calendar' ? Date.UTC(2027, 0, 19, 18, 30) / 1000 : null,
              next_transition_at:
                input.alignment === 'calendar' ? Date.UTC(2027, 0, 19, 18, 30) / 1000 : null,
            }
          : {};
        return rule({
          ...input,
          ...reset,
          id: input.id ?? `qlr_${String.fromCharCode(66 + index).repeat(21)}Q`,
          remaining:
            input.metric === 'calls'
              ? String(BigInt(input.limit) > retained ? BigInt(input.limit) - retained : 0n)
              : '0',
        });
      });
      const receipt = { donation_id: '7', key_id: '11', donation_revision: state.revision };
      receipts.set(key, receipt);
      if (state.firstWriteUnknown && state.writes.length === 1)
        return json(route, { invalid: true });
      return json(route, receipt);
    }
    return route.fallback();
  });
  const open = async () => {
    if (role === 'owner') {
      await page.goto(`${origin}/charity/donations/7`);
      await page.locator('.economy-donation-details > summary').click();
    } else {
      await page.goto(role === 'admin' ? `${origin}/charity` : `${origin}/steward?tab=charity`);
      await page
        .getByRole('button', { name: locale === 'zh' ? '审核' : 'Review', exact: true })
        .click();
    }
    await expect(page.locator('.recurring-limits-disclosure')).toHaveCount(1);
  };
  const expand = () => page.locator('.recurring-limits-disclosure > summary').click();
  const check = async (name: string) => {
    await expect(page.locator('html')).toHaveAttribute('lang', locale === 'zh' ? 'zh-CN' : 'en');
    await expect(page.locator('html')).toHaveAttribute('data-theme', theme);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    await assertNoSensitiveBrowserPersistence(page, [MARKER]);
    expect(errors).toEqual([]);
    if (process.env.NONBIRI_VISUAL_DIR) {
      const dir = resolve(process.env.NONBIRI_VISUAL_DIR);
      await mkdir(dir, { recursive: true });
      await page
        .locator('.recurring-limits-disclosure')
        .screenshot({ path: resolve(dir, `${name}.png`) });
    }
  };
  return { state, open, expand, check };
}

test.describe('recurring limits in the complete charity pages', () => {
  test.use({ timezoneId: 'America/New_York' });

  test('admin retries the initial read, edits one key, and retains the saved rules through refresh', async ({
    context,
    page,
  }) => {
    const flow = await prepare(context, page, 'admin', 'en', 'light', 1280);
    flow.state.firstReadInvalid = true;
    flow.state.pauseSave = true;
    await flow.open();
    expect(flow.state.reads).toBe(0);
    await flow.expand();
    await page
      .locator('.recurring-limits-disclosure')
      .getByRole('button', { name: 'Retry', exact: true })
      .click();
    const editor = page.locator('.recurring-limits');
    await editor.getByLabel('Limit', { exact: true }).fill('999');
    await editor.getByRole('button', { name: 'Discard changes', exact: true }).click();
    await expect(editor.getByLabel('Limit', { exact: true })).toHaveValue('100');
    await editor.getByLabel('Limit', { exact: true }).fill('150');
    await editor.getByRole('combobox', { name: 'Period', exact: true }).selectOption('week');
    await editor.getByRole('combobox', { name: 'Starts at', exact: true }).selectOption('calendar');
    await editor.getByRole('combobox', { name: 'Week starts on', exact: true }).selectOption('3');
    await editor.getByLabel('Time zone', { exact: true }).fill(MARKER);
    await expect(
      editor.getByRole('button', { name: 'Save recurring limits', exact: true }),
    ).toBeDisabled();
    await editor.getByLabel('Time zone', { exact: true }).fill('Asia/Kolkata');
    await expect(editor).toContainText('UTC+5:30');
    await expect(editor).toContainText('changes structure');
    await editor.getByRole('button', { name: 'Save recurring limits', exact: true }).click();
    await expect.poll(() => flow.state.writes.length).toBe(1);
    await expect(editor.getByRole('button', { name: 'Saving…', exact: true })).toBeDisabled();
    await expect(editor.getByLabel('Limit', { exact: true })).toBeDisabled();
    flow.state.releaseSave?.();
    await expect(editor).toContainText('Recurring limits saved.');
    expect(flow.state.writes[0]?.payload).toEqual({
      expected_revision: '7',
      rules: [
        {
          id: RULE_ID,
          mode: 'reset',
          interval: 'week',
          alignment: 'calendar',
          time_zone: 'Asia/Kolkata',
          week_starts_on: 3,
          metric: 'calls',
          limit: '150',
        },
      ],
    });
    expect(flow.state.writes[0]?.key).toMatch(/^[A-Za-z0-9_-]{22,128}$/);
    await editor.getByRole('button', { name: /^Collapse rule:/ }).click();
    await expect(editor.locator('.recurring-limits__summary-text')).toContainText(
      'Remaining: 150 calls',
    );
    await flow.check('quota-admin-1280-light-en');
    await page.setViewportSize({ width: 320, height: 900 });
    await flow.check('quota-admin-320-light-en');
    await flow.open();
    await flow.expand();
    await expect(editor.getByLabel('Limit', { exact: true })).toHaveValue('150');
    await expect(editor.getByLabel('Time zone', { exact: true })).toHaveValue('Asia/Kolkata');
    expect(flow.state.writes).toHaveLength(1);
  });

  test('admin folds an unknown save and safely retries the original command', async ({
    context,
    page,
  }) => {
    const flow = await prepare(context, page, 'admin', 'en', 'dark', 390);
    flow.state.firstWriteUnknown = true;
    await flow.open();
    await flow.expand();
    const editor = page.locator('.recurring-limits');
    await editor.getByLabel('Limit', { exact: true }).fill('120');
    await editor.getByRole('button', { name: 'Save recurring limits', exact: true }).click();
    await expect(editor).toContainText('The save result is unknown.');
    await expect(
      editor.getByRole('button', { name: 'Save recurring limits', exact: true }),
    ).toBeDisabled();
    await flow.expand();
    await flow.expand();
    await expect(editor).toContainText('The save result is unknown.');
    await editor.getByRole('button', { name: 'Retry the same save', exact: true }).click();
    await expect(editor).toContainText('Recurring limits saved.');
    expect(flow.state.writes).toHaveLength(2);
    expect(flow.state.writes[1]).toEqual(flow.state.writes[0]);
    expect(flow.state.revision).toBe('8');
    await flow.check('quota-admin-390-dark-en');
  });

  test('steward compares a concurrent edit and explicitly keeps the Chinese draft', async ({
    context,
    page,
  }) => {
    const flow = await prepare(context, page, 'steward', 'zh', 'dark', 320);
    flow.state.firstWriteConflict = true;
    await flow.open();
    await flow.expand();
    const editor = page.locator('.recurring-limits');
    await editor.getByLabel('上限', { exact: true }).fill('130');
    await editor.getByRole('button', { name: '保存循环限量', exact: true }).click();
    const comparison = editor.locator('.recurring-limits__comparison');
    await expect(comparison).toContainText('Asia/Tokyo');
    await expect(comparison).toContainText('UTC');
    await expect(editor.getByRole('button', { name: '保存循环限量', exact: true })).toBeDisabled();
    await flow.check('quota-steward-conflict-320-dark-zh');
    await editor.getByRole('button', { name: '保留我的草稿', exact: true }).click();
    await editor.getByRole('button', { name: '保存循环限量', exact: true }).click();
    await expect(editor).toContainText('循环限量已保存');
    expect(flow.state.writes[1]?.payload.expected_revision).toBe('8');
    expect(flow.state.writes[1]?.payload.rules[0]?.time_zone).toBe('UTC');
    expect(flow.state.writes[1]?.key).not.toBe(flow.state.writes[0]?.key);
    await flow.check('quota-steward-saved-320-dark-zh');
  });
});

test.describe('ordinary donor quota details', () => {
  test.use({ timezoneId: 'Asia/Kolkata' });
  test('owner reads exact large usage and business times without any write controls', async ({
    context,
    page,
  }) => {
    const flow = await prepare(context, page, 'owner', 'en', 'dark', 320);
    const maximum = ((1n << 128n) - 1n).toString();
    flow.state.rules = [
      rule({ limit: maximum, used: maximum, reserved: '0', remaining: '0', state: 'limited' }),
    ];
    await flow.open();
    expect(flow.state.reads).toBe(0);
    await flow.expand();
    const editor = page.locator('.recurring-limits');
    await expect(editor).toContainText('Read-only view for this key owner.');
    await expect(editor).toContainText('Browser time');
    await expect(editor).toContainText('UTC');
    await expect(editor.getByTitle(maximum)).toHaveCount(2);
    await expect(
      editor.getByRole('button', { name: 'Save recurring limits', exact: true }),
    ).toHaveCount(0);
    await expect(editor.locator('input,select')).toHaveCount(0);
    await flow.check('quota-owner-320-dark-en');
    await page.setViewportSize({ width: 1280, height: 900 });
    await flow.check('quota-owner-1280-dark-en');
    expect(flow.state.writes).toHaveLength(0);
  });
});
