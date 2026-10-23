import { readFileSync } from 'node:fs';
import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import commonEn from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import commonZh from '../../src/shared/i18n/common/zh.json' with { type: 'json' };
import userEn from '../../src/user/i18n/en.json' with { type: 'json' };
import userZh from '../../src/user/i18n/zh.json' with { type: 'json' };
import { numberedPage } from './numbered-fixtures';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import { mockPublicConfig, mockRoleSession } from './support';
import { expect, test, type Page } from './test';
type Kind = 'personal' | 'admin' | 'steward';
type Row = { name: string; idle_seconds: number; format: string };
const personalBase = JSON.parse(
  readFileSync(
    resolve(process.cwd(), '..', 'internal/resources/testdata/manual_update.json'),
    'utf8',
  ),
).affected_models[0].model;
const charityBase = {
  recurring_discount: { enabled: false, time_zone: 'UTC', default_percent: 100, periods: [] },
  effective_discount: { source: 'none', percent: 100, next_change_at: null },
  server_now: 1800000000,
  model_types: ['embeddings'],
  route_strategy: 'cache_balanced',
  id: '31',
  provider: 'Provider',
  model: 'model',
  full_name: '[公益]Provider/model',
  enabled: true,
  allowed_levels: [1, 2, 3, 4, 5],
  public_description: '',
  pricing: { mode: 'per_request', user_price: '1', donor_reward: '0' },
  discount: { enabled: false, percent: 0, start_at: null, end_at: null },
  transport_rule: 'passthrough',
  flatten_tool_calls: false,
  cloudflare_proxy_enabled: false,
  cloudflare_proxy_mode: 'legacy',
  revision: '1',
  binding_revision: '0',
  binding_count: '0',
  rolling_success: { sample_count: '0', success_count: '0', percent: null },
  created_at: 1,
  updated_at: 1,
};
const projection = () => ({
  revision: '1',
  forward_headers: { mode: 'replace', values: [] },
  fixed_headers: { mode: 'replace', values: {} },
  body_defaults: { mode: 'replace', values: {} },
  body_forced: { mode: 'replace', values: {} },
  native_extension_paths: { mode: 'replace', values: [] },
  excluded_request_fields: [],
});
async function fixture(page: Page, kind: Kind, configured = false) {
  const station = kind === 'admin' ? 'admin' : 'user';
  const origin = kind === 'admin' ? ADMIN_ORIGIN : USER_ORIGIN;
  const root = kind === 'personal' ? '/api' : kind === 'admin' ? '/admin/api' : '/api/steward';
  const collection = root + (kind === 'personal' ? '/models' : '/charity-models');
  await mockPublicConfig(page, station);
  await mockRoleSession(
    page,
    station,
    kind === 'personal' ? 'user' : kind === 'admin' ? 'admin' : 'level6',
  );
  let current: Record<string, unknown> = {
    ...(kind === 'personal' ? personalBase : charityBase),
    binding_count: '0',
    binding_revision: '0',
    session_headers: configured
      ? [
          { name: 'x-session-0', idle_seconds: 0, format: 'opencode_part' },
          { name: 'x-session-1', idle_seconds: 300, format: 'opencode_session' },
        ]
      : [],
  };
  let created = configured;
  let adaptation: Record<string, unknown> = projection();
  const state = {
    failNext: false,
    writes: [] as Record<string, unknown>[],
    adaptationWrites: [] as Record<string, unknown>[],
  };
  await page.route(origin + root + '/**', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.pathname.endsWith('/request-adaptation')) {
      if (request.method() === 'PUT') {
        const body = request.postDataJSON();
        state.adaptationWrites.push(body);
        adaptation = { ...projection(), ...body, revision: '2' };
        delete adaptation.expected_revision;
      }
      return route.fulfill({ json: adaptation });
    }
    if (url.pathname === collection || url.pathname === collection + '/' + current.id) {
      if (request.method() === 'POST' || request.method() === 'PATCH') {
        const body = request.postDataJSON();
        state.writes.push(body);
        if (state.failNext) {
          state.failNext = false;
          return route.fulfill({
            status: 400,
            json: { error: { code: 'invalid_request', message: 'Invalid header configuration.' } },
          });
        }
        current = {
          ...current,
          ...body,
          revision: String(BigInt(String(current.revision)) + 1n),
          full_name: `${kind === 'personal' ? '' : '[公益]'}${body.provider}/${body.model}`,
          session_headers: (body.session_headers as Row[])
            .map((row) => ({ ...row, name: row.name.trim().toLowerCase() }))
            .sort((a, b) => a.name.localeCompare(b.name)),
        };
        delete current.expected_revision;
        created = true;
        return route.fulfill({ status: request.method() === 'POST' ? 201 : 200, json: current });
      }
      return route.fulfill({
        json:
          url.pathname === collection
            ? numberedPage(created ? [current] : [], url.searchParams)
            : current,
      });
    }
    if (url.pathname.endsWith('/bindings'))
      return route.fulfill({ json: { bindings: [], binding_revision: '0' } });
    if (
      url.pathname.endsWith('/binding-candidates') ||
      ['/endpoints', '/donations', '/donation-sources', '/charity-price-templates'].some(
        (suffix) => url.pathname === root + suffix,
      )
    )
      return route.fulfill({ json: numberedPage([], url.searchParams) });
    return route.fallback();
  });
  return {
    origin,
    state,
    get current() {
      return current;
    },
  };
}

for (const kind of ['personal', 'admin', 'steward'] as const) {
  for (const width of [1440, 390]) {
    for (const theme of ['light', 'dark'] as const) {
      test(`${kind} session headers create/reopen/cancel/clear at ${width}px ${theme}`, async ({
        page,
      }) => {
        const locale = width === 390 && theme === 'dark' ? 'zh' : 'en';
        const common = locale === 'zh' ? commonZh : commonEn;
        const user = locale === 'zh' ? userZh : userEn;
        const core = user.user.core;
        const copy = common.common.operations.charity;
        const headers = copy.sessionHeaders;
        const modelCopy = common.requestAdaptation.model;
        const personal = kind === 'personal';
        await page.setViewportSize({ width, height: 900 });
        await page.addInitScript(
          ({ theme, locale }) => {
            localStorage.setItem('nb.lang', locale);
            localStorage.setItem('nb.theme', theme);
          },
          { theme, locale },
        );
        const errors: string[] = [];
        page.on('pageerror', (error) => errors.push(error.name));
        const timeRequests: Array<{ path: string; status: number }> = [];
        page.on('response', (response) => {
          if (response.url().includes('time-context'))
            timeRequests.push({
              path: new URL(response.url()).pathname,
              status: response.status(),
            });
        });
        const f = await fixture(page, kind);
        const destination = personal
          ? '/models'
          : (kind === 'admin' ? '/charity?' : '/steward?tab=charity&') +
            'charity_section=models&model_workspace=new';
        await page.goto(f.origin + destination);
        if (personal)
          await page
            .locator('[data-component="page-header"]')
            .getByRole('button', { name: core['models.create'], exact: true })
            .click();
        const nameEditor = personal ? page.locator('.model-editor') : page;
        await nameEditor
          .getByLabel(personal ? core['models.provider'] : user.user.steward.provider, {
            exact: true,
          })
          .fill('headers');
        await nameEditor
          .getByLabel(personal ? core['models.model'] : user.user.steward.model, { exact: true })
          .fill('example');
        const sessionFold = page.locator('summary').filter({ hasText: headers.title });
        await sessionFold.click();
        const formats = [
          'uuid_v4',
          'hex_32',
          'opencode_session',
          'opencode_message',
          'opencode_part',
        ];
        for (let i = 0; i < 2; i++) {
          await page.getByRole('button', { name: headers.add, exact: true }).click();
          await page
            .getByLabel(headers.name.replace('{{n}}', String(i + 1)), { exact: true })
            .fill(`X-Session-${i}`);
          await page
            .getByLabel(headers.idle, { exact: true })
            .nth(i)
            .fill(i === 0 ? '0' : '');
          const select = page.getByLabel(headers.format, { exact: true }).nth(i);
          expect(
            await select
              .locator('option')
              .evaluateAll((options) =>
                options.map((option) => (option as HTMLOptionElement).value),
              ),
          ).toEqual(formats);
          for (const format of formats) await select.selectOption(format);
          if (i === 1) await select.selectOption('opencode_session');
        }
        const saveModel = () =>
          page.getByRole('button', {
            name: personal
              ? core['common.save']
              : common.common.workflow.saveTarget.replace('{{id}}', String(f.current.id)),
            exact: true,
          });
        const createModel = page.getByRole('button', {
          name: personal ? core['common.save'] : user.user.steward.newModel,
          exact: true,
        });
        if (!personal && (await createModel.isDisabled())) {
          await test.info().attach('charity-model-validation', {
            contentType: 'application/json',
            body: JSON.stringify({
              errors: await page.locator('.field-error').allTextContents(),
              timeRequests,
              timeNotice: await page.locator('.time-context-notice').allTextContents(),
              dates: await page.locator('input[type="datetime-local"]').evaluateAll((inputs) =>
                inputs.map((element) => {
                  const input = element as HTMLInputElement;
                  return {
                    value: input.value,
                    disabled: input.disabled,
                    ariaInvalid: input.getAttribute('aria-invalid'),
                  };
                }),
              ),
              provider: await nameEditor
                .getByLabel(user.user.steward.provider, { exact: true })
                .inputValue(),
              model: await nameEditor
                .getByLabel(user.user.steward.model, { exact: true })
                .inputValue(),
              session_headers: await Promise.all(
                [0, 1].map(async (index) => ({
                  name: await page
                    .getByLabel(headers.name.replace('{{n}}', String(index + 1)), { exact: true })
                    .inputValue(),
                  idle_seconds: await page
                    .getByLabel(headers.idle, { exact: true })
                    .nth(index)
                    .inputValue(),
                  format: await page
                    .getByLabel(headers.format, { exact: true })
                    .nth(index)
                    .inputValue(),
                })),
              ),
            }),
          });
        }
        await createModel.click();
        await expect.poll(() => f.state.writes.length).toBe(1);
        expect(f.state.writes[0].session_headers).toEqual([
          { name: 'X-Session-0', idle_seconds: 0, format: 'opencode_part' },
          { name: 'X-Session-1', idle_seconds: 300, format: 'opencode_session' },
        ]);
        if (!personal)
          expect(f.state.writes[0].discount).toEqual({
            enabled: false,
            percent: 0,
            start_at: null,
            end_at: null,
          });
        if (personal)
          await expect(
            page.getByRole('button', { name: core['models.editModel'], exact: true }),
          ).toBeVisible();
        else await expect(saveModel()).toBeVisible();
        await page.reload();
        if (personal)
          await page.getByRole('button', { name: core['models.editModel'], exact: true }).click();
        const idle = () => page.getByLabel(headers.idle, { exact: true }).first();
        await expect(idle()).toHaveValue('0');
        await expect(page.getByLabel(headers.idle, { exact: true }).nth(1)).toHaveValue('300');
        await expect(page.getByLabel(headers.format, { exact: true }).first()).toHaveValue(
          'opencode_part',
        );

        const evidence = process.env.NONBIRI_VISUAL_DIR;
        const capture =
          evidence &&
          ((width === 1440 && theme === 'light') || (width === 390 && theme === 'dark'));
        if (capture) {
          await mkdir(evidence, { recursive: true });
          await sessionFold.evaluate((element) => element.scrollIntoView({ block: 'start' }));
          await page.evaluate(() => window.scrollBy(0, -100));
          await page.screenshot({
            path: resolve(evidence, `${kind}-${width}-${theme}-session.png`),
          });
        }
        await idle().fill('24');
        if (personal) {
          await page.getByRole('button', { name: core['common.cancel'], exact: true }).click();
          await page.getByRole('button', { name: core['models.editModel'], exact: true }).click();
        } else await page.getByRole('button', { name: copy.reloadModel, exact: true }).click();
        await expect(idle()).toHaveValue('0');
        f.state.failNext = true;
        await idle().fill('600');
        await page
          .getByLabel(headers.format, { exact: true })
          .first()
          .selectOption('opencode_message');
        await saveModel().click();
        await expect.poll(() => f.state.writes.length).toBe(2);
        await expect(saveModel()).toBeEnabled();
        await expect(idle()).toHaveValue('600');
        await expect(page.getByLabel(headers.format, { exact: true }).first()).toHaveValue(
          'opencode_message',
        );
        for (let i = 0; i < 2; i++)
          await page
            .getByRole('button', { name: headers.remove.replace('{{n}}', '1'), exact: true })
            .click();
        await saveModel().click();
        await expect.poll(() => f.current.session_headers).toEqual([]);
        if (personal)
          await expect(
            page.getByRole('button', { name: core['models.editModel'], exact: true }),
          ).toBeVisible();
        await page.reload();
        if (personal)
          await page.getByRole('button', { name: core['models.editModel'], exact: true }).click();
        await expect(
          page.getByLabel(headers.name.replace('{{n}}', '1'), { exact: true }),
        ).toHaveCount(0);
        if (personal)
          await page.getByRole('button', { name: core['common.cancel'], exact: true }).click();
        const adaptation = page.locator('summary').filter({ hasText: modelCopy.title }).first();
        await adaptation.click();
        await expect(
          page.getByRole('group', { name: modelCopy.headers, exact: true }),
        ).toBeVisible();
        await expect(page.getByRole('group', { name: modelCopy.body, exact: true })).toBeVisible();
        const forwarded = page.getByRole('group', { name: modelCopy.forwardHeaders, exact: true });
        const adaptationSave = page.getByRole('button', { name: modelCopy.save, exact: true });
        if (kind === 'steward') {
          await expect(forwarded.getByRole('textbox')).toBeDisabled();
          await expect(adaptationSave).toHaveCount(0);
        } else {
          await forwarded.getByRole('textbox').fill('X-Trace-ID');
          await adaptationSave.click();
          await expect.poll(() => f.state.adaptationWrites.length).toBe(1);
          expect(f.state.writes).toHaveLength(3);
          await expect(forwarded.getByRole('textbox')).toHaveValue(/x-trace-id/i);
        }
        if (capture) {
          await page
            .getByRole('group', { name: modelCopy.headers, exact: true })
            .evaluate((element) => element.scrollIntoView({ block: 'start' }));
          await page.evaluate(() => window.scrollBy(0, -100));
          await page.screenshot({
            path: resolve(evidence, `${kind}-${width}-${theme}-adaptation.png`),
          });
        }
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
          true,
        );
        await expect(page.locator('html')).toHaveAttribute('data-theme', theme);
        expect(errors).toEqual([]);
      });
    }
  }
}

for (const kind of ['admin', 'steward'] as const) {
  for (const scenario of [
    { width: 1440, theme: 'light' },
    { width: 390, theme: 'dark' },
  ] as const) {
    test(`charity header layout ${kind} ${scenario.width}px ${scenario.theme}`, async ({
      page,
    }) => {
      const locale = scenario.width === 390 ? 'zh' : 'en';
      const common = locale === 'zh' ? commonZh : commonEn;
      const headers = common.common.operations.charity.sessionHeaders;
      const modelCopy = common.requestAdaptation.model;
      await page.setViewportSize({ width: scenario.width, height: 900 });
      await page.addInitScript(
        ({ theme, locale }) => {
          localStorage.setItem('nb.lang', locale);
          localStorage.setItem('nb.theme', theme);
        },
        { theme: scenario.theme, locale },
      );
      const f = await fixture(page, kind, true);
      await page.goto(
        f.origin +
          (kind === 'admin' ? '/charity?' : '/steward?tab=charity&') +
          'charity_section=models&charity_model=31',
      );
      await expect(page.getByLabel(headers.idle, { exact: true }).first()).toHaveValue('0');
      const sessionFold = page.locator('summary').filter({ hasText: headers.title });
      const unframed = sessionFold.locator('..').locator('fieldset').first();
      expect(
        await unframed.evaluate((element) => {
          const style = getComputedStyle(element);
          return [style.borderTopWidth, style.paddingTop, style.marginTop];
        }),
      ).toEqual(['0px', '0px', '0px']);
      const evidence = process.env.NONBIRI_VISUAL_DIR;
      const capture = async (locator: ReturnType<Page['locator']>, suffix: string) => {
        if (!evidence) return;
        await mkdir(evidence, { recursive: true });
        await locator.evaluate((element) => element.scrollIntoView({ block: 'start' }));
        await page.evaluate(() => window.scrollBy(0, -100));
        await page.screenshot({
          path: resolve(evidence, `${kind}-${scenario.width}-${scenario.theme}-${suffix}.png`),
        });
      };
      await capture(sessionFold, 'session');
      await page.locator('summary').filter({ hasText: modelCopy.title }).first().click();
      for (const name of [modelCopy.headers, modelCopy.body]) {
        expect(
          await page
            .getByRole('group', { name, exact: true })
            .evaluate((element) => getComputedStyle(element).borderTopWidth),
        ).toBe('0px');
      }
      const forwarded = page
        .getByRole('group', { name: modelCopy.forwardHeaders, exact: true })
        .getByRole('textbox');
      if (kind === 'steward') await expect(forwarded).toBeDisabled();
      else await expect(forwarded).toBeEnabled();
      await capture(
        page.getByRole('group', { name: modelCopy.headers, exact: true }),
        'adaptation',
      );
      expect(f.state.writes).toEqual([]);
      expect(f.state.adaptationWrites).toEqual([]);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
    });
  }
}
