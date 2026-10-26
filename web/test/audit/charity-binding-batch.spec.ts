import { readFileSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
import { resolve } from 'node:path';
import {
  expect,
  test,
  type Browser,
  type BrowserContext,
  type Page,
  type TestInfo,
} from '@playwright/test';
import common from '../../src/shared/i18n/common/en.json' with { type: 'json' };

type Cookie = { Name: string; Value: string };
interface Fixture {
  admin_url: string;
  user_url: string;
  admin_cookie: Cookie;
  users: { id: string; level: number; cookie: Cookie }[];
  management: {
    binding_donation_id: string;
    binding_key_ids: string[];
    binding_model_ids: string[];
    binding_channel_id: string;
    custom_binding_donation_id: string;
    custom_binding_key_ids: string[];
    custom_binding_model_ids: string[];
    custom_binding_channel_id: string;
  };
}
const batch = common.common.bindingBatch;
const presets = common.common.bindingPresets;
function fixture(): Fixture {
  return JSON.parse(readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8')) as Fixture;
}
test.skip(
  process.env.NONBIRI_MANAGEMENT_BROWSER !== '1',
  'Requires the isolated management fixture.',
);
async function session(browser: Browser, role: 'admin' | 5 | 6) {
  const state = fixture(),
    origin = role === 'admin' ? state.admin_url : state.user_url;
  const cookie =
    role === 'admin' ? state.admin_cookie : state.users.find((user) => user.level === role)!.cookie;
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    reducedMotion: 'reduce',
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
    localStorage.setItem('nb.lang', 'en');
    localStorage.setItem('nb.theme', 'light');
  });
  return context;
}
async function read(context: BrowserContext, origin: string, path: string) {
  const response = await context.request.get(origin + path, { headers: { Origin: origin } });
  expect(response.ok(), `${path}: ${response.status()}`).toBe(true);
  return response.json();
}
async function visuals(page: Page, info: TestInfo, name: string, focus: () => Promise<void>) {
  for (const theme of ['light', 'dark'])
    for (const width of [1440, 390]) {
      await page.evaluate((value) => {
        document.documentElement.dataset.theme = value;
      }, theme);
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
      await focus();
      if (name.startsWith('binding-')) {
        const layout = page.locator('.charity-donations');
        await expect(layout.locator(':scope > .charity-donations__list')).toBeHidden();
        const container = (await layout.boundingBox())!;
        const detail = (await layout.locator(':scope > .ops-detail-target').boundingBox())!;
        expect(Math.abs(detail.x - container.x)).toBeLessThanOrEqual(1);
        expect(detail.width).toBeGreaterThanOrEqual(container.width - 2);
      }
      if (name === 'binding-current-marker') {
        await expect(page.getByText(batch.boundHere, { exact: true })).toBeInViewport();
        const lines = await page.locator('.ops-picker-context > *').evaluateAll((nodes) =>
          nodes.map((node) => {
            const { top, bottom } = node.getBoundingClientRect();
            return { top, bottom };
          }),
        );
        expect(lines[1]!.top).toBeGreaterThanOrEqual(lines[0]!.bottom);
        expect(lines[2]!.top).toBeGreaterThanOrEqual(lines[1]!.bottom);
      }
      if (name === 'binding-revision-conflict') {
        await expect(
          page.getByRole('heading', { name: common.common.error, exact: true }),
        ).toHaveCount(0);
        if (width === 390) {
          const note = page
            .locator('.nb-note')
            .filter({ hasText: common.common.workflow.bindingConflict });
          const copy = (await note.locator(':scope > div').boundingBox())!;
          const action = (await note.getByRole('button').boundingBox())!;
          expect(copy.width).toBeGreaterThan(250);
          expect(action.y).toBeGreaterThanOrEqual(copy.y + copy.height);
        }
      }
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      if (name === 'review-return-6' && width === 390) {
        const widerFont = await page.addStyleTag({
          content:
            '.charity-management, .charity-management :is(input, select, textarea, button) { font-family: Verdana, sans-serif; }',
        });
        try {
          expect(
            await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
          ).toBe(true);
        } finally {
          await widerFont.evaluate((node) => node.parentNode?.removeChild(node));
        }
      }
      await page.screenshot({
        path: process.env.NONBIRI_VISUAL_DIR
          ? resolve(process.env.NONBIRI_VISUAL_DIR, `${name}-${theme}-${width}.png`)
          : info.outputPath(`${name}-${theme}-${width}.png`),
        animations: 'disabled',
      });
    }
  await page.setViewportSize({ width: 1440, height: 900 });
}
async function searchModels(page: Page, name: string) {
  const input = page.getByRole('searchbox', {
    name: common.common.operations.charity.searchModels,
  });
  await input.fill(name);
  await input
    .locator('xpath=ancestor::form')
    .getByRole('button', { name: common.common.search, exact: true })
    .click();
}

test('model-centric binding picker reaches bound keys through the general source pages', async ({
  browser,
}) => {
  const state = fixture(),
    context = await session(browser, 'admin');
  try {
    const page = await context.newPage();
    const sourceReads: URL[] = [];
    page.on('request', (request) => {
      const url = new URL(request.url());
      if (url.pathname.includes('/donation-sources')) sourceReads.push(url);
    });
    await page.goto(
      `${state.admin_url}/charity?charity_section=models&charity_model=${state.management.binding_model_ids[0]}`,
    );
    const picker = page.locator('.ops-binding-picker');
    const search = picker.getByRole('searchbox', {
      name: common.common.operations.charity.searchSources,
    });
    await search.fill('Binding browser service');
    await search
      .locator('xpath=ancestor::form')
      .getByRole('button', { name: common.common.search, exact: true })
      .click();
    await picker.getByRole('button').filter({ hasText: 'Binding browser service' }).click();
    await picker
      .getByRole('region', { name: common.common.operations.charity.chooseKey })
      .getByRole('button', { name: /Binding browser key 1/ })
      .click();
    await expect(picker.getByRole('checkbox', { name: /binding-up-0/ })).toBeDisabled();
    await expect(
      picker
        .getByRole('checkbox', { name: /binding-up-0/ })
        .locator('..')
        .getByText(batch.boundHere, { exact: true }),
    ).toBeVisible();
    expect(sourceReads.length).toBeGreaterThan(1);
    expect(sourceReads.every((url) => !url.searchParams.has('include_bound'))).toBe(true);
  } finally {
    await context.close();
  }
});

test('key binding collects multiple targets, shows exact existing bindings, and preserves the basket on return', async ({
  browser,
}, info) => {
  const state = fixture(),
    data = state.management;
  const context = await session(browser, 'admin');
  try {
    const page = await context.newPage();
    await page.goto(
      `${state.admin_url}/charity?charity_section=donations&donation_id=${data.binding_donation_id}&binding_key=${data.binding_key_ids[0]}`,
    );
    await searchModels(page, 'binding-browser');
    await page
      .getByRole('row')
      .filter({ hasText: '[公益]binding-browser/target-0' })
      .getByRole('button', { name: batch.chooseUpstream })
      .click();
    await expect(page.getByRole('checkbox', { name: /binding-up-0/ })).toBeDisabled();
    await expect(page.getByText(batch.boundHere, { exact: true })).toBeVisible();
    await visuals(page, info, 'binding-current-marker', () =>
      page.getByText(batch.boundHere, { exact: true }).scrollIntoViewIfNeeded(),
    );
    await page.getByRole('checkbox', { name: /binding-up-1/ }).check();
    await page.getByRole('button', { name: batch.backTargets }).click();
    await page
      .getByRole('row')
      .filter({ hasText: '[公益]binding-browser/target-1' })
      .getByRole('button', { name: batch.chooseUpstream })
      .click();
    await page.getByRole('checkbox', { name: /binding-up-2/ }).check();
    await page.getByRole('button', { name: batch.backTargets }).click();
    await expect(page.getByRole('region', { name: batch.selected })).toContainText(
      '[公益]binding-browser/target-0',
    );
    await expect(page.getByRole('region', { name: batch.selected })).toContainText(
      '[公益]binding-browser/target-1',
    );
    await visuals(page, info, 'binding-multi-target-basket', () =>
      page.getByRole('region', { name: batch.selected }).scrollIntoViewIfNeeded(),
    );
    const current = await read(
      context,
      state.admin_url,
      `/admin/api/charity-models/${data.binding_model_ids[0]}/bindings`,
    );
    const concurrent = await context.request.post(
      `${state.admin_url}/admin/api/charity-models/${data.binding_model_ids[0]}/bindings/batch-receipt`,
      {
        headers: { Origin: state.admin_url, 'Idempotency-Key': randomUUID() },
        data: {
          expected_binding_revision: current.binding_revision,
          selections: [
            { donation_key_id: data.binding_key_ids[2], upstream_model_id: 'binding-up-5' },
          ],
        },
      },
    );
    expect(concurrent.ok()).toBe(true);
    await page
      .getByRole('button', { name: common.common.workflow.addCount.replace('{{count}}', '2') })
      .click();
    await expect(
      page.getByText(common.common.workflow.bindingConflict, { exact: true }),
    ).toBeVisible();
    await visuals(page, info, 'binding-revision-conflict', () =>
      page
        .getByRole('button', { name: common.common.workflow.refreshSelection })
        .scrollIntoViewIfNeeded(),
    );
    await page.getByRole('button', { name: common.common.workflow.refreshSelection }).click();
    await page
      .getByRole('button', { name: common.common.workflow.addCount.replace('{{count}}', '2') })
      .click();
    await expect(page.getByRole('region', { name: batch.results })).toContainText(
      batch.status.added,
    );
    const first = await read(
      context,
      state.admin_url,
      `/admin/api/charity-models/${data.binding_model_ids[0]}/bindings`,
    );
    const second = await read(
      context,
      state.admin_url,
      `/admin/api/charity-models/${data.binding_model_ids[1]}/bindings`,
    );
    expect(
      first.bindings.some(
        (binding: { donation_key_id: string; upstream_model_id: string }) =>
          binding.donation_key_id === data.binding_key_ids[0] &&
          binding.upstream_model_id === 'binding-up-1',
      ),
    ).toBe(true);
    expect(
      second.bindings.some(
        (binding: { donation_key_id: string; upstream_model_id: string }) =>
          binding.donation_key_id === data.binding_key_ids[0] &&
          binding.upstream_model_id === 'binding-up-2',
      ),
    ).toBe(true);
    await page.getByRole('button', { name: common.common.workflow.returnDonation }).click();
    await expect(
      page.getByRole('heading', { name: common.common.operations.charity.donationKeys }),
    ).toBeVisible();
  } finally {
    await context.close();
  }
});

test('completed key binding returns to the donation list and reopens a fresh review', async ({
  browser,
}, info) => {
  const state = fixture(),
    data = state.management;
  for (const role of ['admin', 6] as const) {
    const context = await session(browser, role),
      origin = role === 'admin' ? state.admin_url : state.user_url,
      prefix = role === 'admin' ? '/admin/api' : '/api/steward',
      keyIndex = role === 'admin' ? 0 : 1;
    try {
      const page = await context.newPage();
      const path = role === 'admin' ? '/charity?' : '/steward?tab=charity&';
      await page.goto(
        origin +
          path +
          'charity_section=donations&handling=pending&donation_status=approved' +
          '&q=Binding%20preset%20browser%20donation&donations_page=1&donations_page_size=10',
      );
      await page
        .getByRole('button', { name: common.common.operations.charity.openReview, exact: true })
        .click();
      await expect(
        page.getByRole('heading', { name: common.common.donationReview.title, exact: true }),
      ).toBeVisible();
      await page
        .getByRole('button', { name: common.common.connections.modelsAndBindings, exact: true })
        .nth(keyIndex)
        .click();
      await expect(page).toHaveURL(
        new RegExp(`binding_key=${data.binding_key_ids[keyIndex]}(?:&|$)`),
      );
      await searchModels(page, 'binding-browser/target-4');
      await page
        .getByRole('row')
        .filter({ hasText: '[公益]binding-browser/target-4' })
        .getByRole('button', { name: batch.chooseUpstream, exact: true })
        .click();
      await page.getByRole('checkbox', { name: /binding-up-4/ }).check();
      await page
        .getByRole('button', {
          name: common.common.workflow.addCount.replace('{{count}}', '1'),
          exact: true,
        })
        .click();
      await expect(page.getByRole('region', { name: batch.results })).toContainText(
        batch.status.added,
      );
      const saved = await read(
        context,
        origin,
        `${prefix}/charity-models/${data.binding_model_ids[4]}/bindings`,
      );
      expect(
        saved.bindings.some(
          (binding: { donation_key_id: string; upstream_model_id: string }) =>
            binding.donation_key_id === data.binding_key_ids[keyIndex] &&
            binding.upstream_model_id === 'binding-up-4',
        ),
      ).toBe(true);
      await page
        .getByRole('button', { name: common.common.operations.charity.returnToList, exact: true })
        .click();
      const params = new URL(page.url()).searchParams;
      for (const name of ['donation_id', 'binding_donation', 'binding_key', 'binding_target'])
        expect(params.has(name), name).toBe(false);
      expect(Object.fromEntries(params)).toMatchObject({
        handling: 'pending',
        donation_status: 'approved',
        q: 'Binding preset browser donation',
        donations_page: '1',
        donations_page_size: '10',
      });
      await page
        .getByRole('button', { name: common.common.operations.charity.openReview, exact: true })
        .click();
      await expect(
        page.getByRole('heading', { name: common.common.donationReview.title, exact: true }),
      ).toBeVisible();
      await expect(
        page.getByRole('heading', { name: common.common.workflow.addConnections, exact: true }),
      ).toHaveCount(0);
      await page.reload();
      await expect(
        page.getByRole('heading', { name: common.common.donationReview.title, exact: true }),
      ).toBeVisible();
      await visuals(page, info, `review-return-${role}`, () =>
        page
          .getByRole('heading', { name: common.common.donationReview.title, exact: true })
          .scrollIntoViewIfNeeded(),
      );
      await page
        .getByRole('button', { name: common.common.connections.modelsAndBindings, exact: true })
        .nth(keyIndex)
        .click();
      await page
        .getByRole('button', { name: common.common.workflow.returnDonation, exact: true })
        .click();
      await expect(
        page.getByRole('heading', { name: common.common.donationReview.title, exact: true }),
      ).toBeVisible();
      expect(new URL(page.url()).searchParams.get('donation_id')).toBe(data.binding_donation_id);
    } finally {
      await context.close();
    }
  }
});

test('administrator and level 6 configure channel presets and apply missing or repeated items without stopping other keys', async ({
  browser,
}, info) => {
  const state = fixture(),
    data = state.management;
  for (const role of ['admin', 6] as const) {
    const context = await session(browser, role),
      origin = role === 'admin' ? state.admin_url : state.user_url;
    try {
      const page = await context.newPage();
      await page.goto(origin + '/charity/channel-presets');
      const channelSearch = page.getByRole('searchbox', { name: presets.searchChannels });
      await channelSearch.fill('Binding browser service');
      await channelSearch
        .locator('xpath=ancestor::form')
        .getByRole('button', { name: common.common.search, exact: true })
        .click();
      await page
        .getByRole('row')
        .filter({ hasText: 'Binding browser service' })
        .getByRole('button', { name: presets.configure })
        .click();
      if (role === 'admin')
        await visuals(page, info, 'channel-binding-preset-empty', () =>
          page
            .getByRole('heading', { name: 'Binding browser service', exact: true })
            .scrollIntoViewIfNeeded(),
        );
      if (role === 'admin') {
        for (const index of [0, 3]) {
          await page.getByRole('button', { name: presets.addMapping }).click();
          await searchModels(page, 'binding-browser');
          await page
            .getByRole('region', { name: presets.chooseTarget })
            .getByRole('row')
            .filter({ hasText: `[公益]binding-browser/target-${index}` })
            .getByRole('button', { name: common.common.workflow.chooseTarget })
            .click();
          await page
            .getByRole('textbox', {
              name: presets.upstreamLabel.replace('{{number}}', index === 0 ? '1' : '2'),
            })
            .fill(`binding-up-${index}`);
        }
      } else {
        await page
          .getByRole('textbox', { name: presets.upstreamLabel.replace('{{number}}', '1') })
          .fill('binding-up-1');
      }
      // Returning within the page retains the unsaved editor and exact IDs.
      await page.getByRole('button', { name: presets.backChannels }).click();
      await page
        .getByRole('row')
        .filter({ hasText: 'Binding browser service' })
        .getByRole('button', { name: presets.configure })
        .click();
      await expect(
        page.getByRole('textbox', { name: presets.upstreamLabel.replace('{{number}}', '2') }),
      ).toHaveValue('binding-up-3');
      await page.getByRole('button', { name: common.common.save, exact: true }).click();
      await expect(page.getByText(common.common.outcome.saved, { exact: true })).toBeVisible();
      if (role === 'admin')
        await visuals(page, info, 'channel-binding-preset', () =>
          page
            .getByRole('heading', { name: 'Binding browser service', exact: true })
            .scrollIntoViewIfNeeded(),
        );
      if (role === 'admin') {
        const upstream = page.getByRole('textbox', {
          name: presets.upstreamLabel.replace('{{number}}', '1'),
        });
        await upstream.fill('binding-local-draft');
        const presetPath = `/admin/api/charity-binding-presets/${data.binding_channel_id}`;
        const saved = await read(context, origin, presetPath);
        const concurrent = await context.request.put(origin + presetPath, {
          headers: { Origin: origin, 'Idempotency-Key': randomUUID() },
          data: {
            expected_channel_revision: saved.channel_revision,
            expected_preset_revision: saved.preset_revision,
            mappings: saved.mappings.map(
              (
                mapping: { upstream_model_id: string; charity_model_id: string },
                index: number,
              ) => ({
                charity_model_id: mapping.charity_model_id,
                upstream_model_id: index === 0 ? 'binding-up-2' : mapping.upstream_model_id,
              }),
            ),
          },
        });
        expect(concurrent.status()).toBe(200);
        const conflict = page.waitForResponse(
          (response) =>
            response.url().endsWith(presetPath) && response.request().method() === 'PUT',
        );
        await page.getByRole('button', { name: common.common.save, exact: true }).click();
        expect((await conflict).status()).toBe(409);
        await page.getByRole('button', { name: common.common.outcome.reload, exact: true }).click();
        await expect(upstream).toHaveValue('binding-local-draft');
        await page.getByRole('button', { name: common.common.cancel, exact: true }).click();
        await expect(upstream).toHaveValue('binding-up-2');
        await upstream.fill('binding-up-0');
        await page.getByRole('button', { name: common.common.save, exact: true }).click();
        await expect(page.getByText(common.common.outcome.saved, { exact: true })).toBeVisible();
      }
      const donationPath =
        role === 'admin'
          ? '/charity?charity_section=donations'
          : '/steward?tab=charity&charity_section=donations';
      await page.goto(`${origin}${donationPath}&donation_id=${data.binding_donation_id}`);
      await page.getByRole('button', { name: presets.apply, exact: true }).click();
      await expect(page.getByRole('region', { name: batch.results })).toContainText(
        batch.status.already_bound,
      );
      await expect(page.getByRole('region', { name: batch.results })).toContainText(
        batch.status.missing_model,
      );
      if (role === 'admin')
        await visuals(page, info, 'channel-binding-apply-results', () =>
          page.getByRole('region', { name: batch.results }).scrollIntoViewIfNeeded(),
        );
      await page.getByRole('button', { name: presets.apply, exact: true }).click();
      await expect(page.getByRole('region', { name: batch.results })).toContainText(
        batch.resultSummary.replace('{{count}}', '0'),
      );
      if (role === 6) {
        await page.goto(origin + '/charity/channel-presets');
        await page
          .getByRole('searchbox', { name: presets.searchChannels })
          .fill('Binding browser service');
        await page
          .getByRole('searchbox', { name: presets.searchChannels })
          .locator('xpath=ancestor::form')
          .getByRole('button', { name: common.common.search, exact: true })
          .click();
        await page
          .getByRole('row')
          .filter({ hasText: 'Binding browser service' })
          .getByRole('button', { name: presets.configure })
          .click();
        await page
          .getByRole('button', { name: common.common.operations.charity.remove, exact: true })
          .first()
          .click();
        await page
          .getByRole('button', { name: common.common.operations.charity.remove, exact: true })
          .first()
          .click();
        await page.getByRole('button', { name: common.common.save, exact: true }).click();
        await expect(page.getByText(common.common.outcome.saved, { exact: true })).toBeVisible();
      }
    } finally {
      await context.close();
    }
  }
});

test('custom donations select matching or other channel templates without changing their source', async ({
  browser,
}, info) => {
  const state = fixture();
  const data = state.management;
  const admin = await session(browser, 'admin');
  try {
    for (const [channelId, modelIds] of [
      [data.custom_binding_channel_id, data.custom_binding_model_ids],
      [data.binding_channel_id, data.binding_model_ids],
    ] as const) {
      const path = `/admin/api/charity-binding-presets/${channelId}`;
      const saved = await read(admin, state.admin_url, path);
      const response = await admin.request.put(state.admin_url + path, {
        headers: { Origin: state.admin_url, 'Idempotency-Key': randomUUID() },
        data: {
          expected_channel_revision: saved.channel_revision,
          expected_preset_revision: saved.preset_revision,
          mappings: [0, 3].map((index) => ({
            upstream_model_id: `binding-up-${index}`,
            charity_model_id: modelIds[index],
          })),
        },
      });
      expect(response.status()).toBe(200);
    }
    const before = await read(
      admin,
      state.admin_url,
      `/admin/api/donations/${data.custom_binding_donation_id}`,
    );
    for (const role of ['admin', 6] as const) {
      const context = role === 'admin' ? admin : await session(browser, role);
      try {
        const origin = role === 'admin' ? state.admin_url : state.user_url;
        const page = await context.newPage();
        const route =
          role === 'admin'
            ? '/charity?charity_section=donations'
            : '/steward?tab=charity&charity_section=donations';
        await page.goto(`${origin}${route}&donation_id=${data.custom_binding_donation_id}`);
        await page.getByText(presets.applyTitle, { exact: true }).click();
        const picker = page.getByRole('region', { name: presets.choosePreset });
        await expect(picker.getByText(presets.recommended, { exact: true })).toBeVisible();
        await expect(picker.getByRole('row').nth(1)).toContainText('Custom preset browser service');
        if (role === 'admin') {
          await visuals(page, info, 'custom-preset-candidates', () =>
            picker.getByRole('heading').scrollIntoViewIfNeeded(),
          );
          await picker
            .getByRole('searchbox', { name: presets.searchChannels })
            .fill('No such template');
          await picker.getByRole('button', { name: common.common.search, exact: true }).click();
          await expect(picker.getByRole('heading', { name: presets.noCandidates })).toBeVisible();
          await picker.getByRole('searchbox', { name: presets.searchChannels }).fill('');
          await picker.getByRole('button', { name: common.common.search, exact: true }).click();
        }
        const other = picker.getByRole('row').filter({
          has: page.getByText('Binding browser service', { exact: true }),
        });
        await expect(other.getByText(presets.recommended, { exact: true })).toHaveCount(0);
        await other.getByRole('button', { name: presets.selectPreset }).click();
        const apply = page.getByRole('region', { name: presets.applyTitle });
        await expect(apply).toContainText('Binding browser service');
        await apply.getByRole('button', { name: presets.changePreset }).click();
        await other.getByRole('button', { name: presets.selectPreset }).click();
        await apply.getByRole('button', { name: presets.apply, exact: true }).click();
        const results = page.getByRole('region', { name: batch.results });
        await expect(results).toContainText(batch.status.missing_model);
        await expect(results).toContainText(
          role === 'admin' ? batch.status.added : batch.status.already_bound,
        );
        if (role === 'admin')
          await visuals(page, info, 'custom-preset-results', () =>
            results.scrollIntoViewIfNeeded(),
          );
        await page.close();
      } finally {
        if (role !== 'admin') await context.close();
      }
    }
    const after = await read(
      admin,
      state.admin_url,
      `/admin/api/donations/${data.custom_binding_donation_id}`,
    );
    const sources = (donation: { keys: { safe_source: unknown }[] }) =>
      donation.keys.map((key) => key.safe_source);
    expect(sources(after)).toEqual(sources(before));
    expect(
      after.keys.every(
        (key: { safe_source: { kind: string } }) => key.safe_source.kind === 'custom',
      ),
    ).toBe(true);
  } finally {
    await admin.close();
  }
});

test('level 5 keeps its allowed manual targets and cannot open channel presets', async ({
  browser,
}) => {
  const state = fixture(),
    data = state.management,
    context = await session(browser, 5);
  try {
    const options = await read(
      context,
      state.user_url,
      '/api/steward/donations/' +
        data.binding_donation_id +
        '/keys/' +
        data.binding_key_ids[0] +
        '/binding-options?q=binding-browser&page=1&page_size=20',
    );
    expect(options.data.map((row: { charity_model_id: string }) => row.charity_model_id)).toContain(
      data.binding_model_ids[0],
    );
    expect(
      options.data.map((row: { charity_model_id: string }) => row.charity_model_id),
    ).not.toContain(data.binding_model_ids[5]);
    const denied = await context.request.get(
      state.user_url + '/api/steward/charity-binding-presets?page=1&page_size=20',
    );
    expect(denied.status()).toBe(403);
    expect((await denied.json()).error.code).toBe('steward_permission_denied');
    const page = await context.newPage();
    await page.goto(
      state.user_url +
        '/steward?tab=charity&charity_section=models&charity_model=' +
        data.binding_model_ids[0],
    );
    const picker = page.locator('.ops-binding-picker');
    const sourceSearch = picker.getByRole('searchbox', {
      name: common.common.operations.charity.searchSources,
    });
    await sourceSearch.fill('Binding browser service');
    await sourceSearch
      .locator('xpath=ancestor::form')
      .getByRole('button', { name: common.common.search, exact: true })
      .click();
    await picker.getByRole('button').filter({ hasText: 'Binding browser service' }).click();
    await picker
      .getByRole('region', { name: common.common.operations.charity.chooseKey })
      .getByRole('button', { name: /Binding browser key 1/ })
      .click();
    await expect(picker.getByRole('checkbox', { name: /binding-up-0/ })).toBeDisabled();
    await expect(
      picker
        .getByRole('checkbox', { name: /binding-up-0/ })
        .locator('..')
        .getByText(batch.boundHere, { exact: true }),
    ).toBeVisible();
    await page.goto(state.user_url + '/steward?tab=charity');
    await expect(page.getByRole('link', { name: presets.title })).toBeVisible();
    await page.goto(state.user_url + '/charity/channel-presets');
    await expect(
      page.getByText(common.management.permissions.denied, { exact: true }),
    ).toBeVisible();
    await expect(page.getByRole('searchbox', { name: presets.searchChannels })).toHaveCount(0);
  } finally {
    await context.close();
  }
});
