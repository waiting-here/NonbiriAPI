import { mkdirSync, readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { expect, test, type Browser, type BrowserContext } from '@playwright/test';
import copy from '../../src/shared/i18n/common/en.json' with { type: 'json' };

interface User {
  id: string;
  cookie: { Name: string; Value: string };
}
interface Sample {
  donation_id: string;
  description: string;
  category: 'subscription' | 'api_platform';
  channel_name: string;
  base_url: string;
  tags: string[];
}
interface Fixture {
  admin_url: string;
  user_url: string;
  admin_cookie: User['cookie'];
  users: User[];
  donation_context: {
    owner: User;
    stewards: { five: User; six: User };
    donations: Sample[];
  };
}
interface Source {
  category?: string;
  base_url: string;
  endpoint_tags?: string[];
}
interface Detail {
  id: string;
  owner?: unknown;
  keys: { safe_source: Source }[];
}

test.skip(
  process.env.NONBIRI_DONATION_CONTEXT_BROWSER !== '1',
  'Requires the isolated donation context fixture.',
);
const fixture = () =>
  JSON.parse(readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8')) as Fixture;

async function session(
  browser: Browser,
  state: Fixture,
  actor: 'admin' | 'five' | 'six' | 'owner',
) {
  const admin = actor === 'admin';
  const origin = admin ? state.admin_url : state.user_url;
  const cookie = admin
    ? state.admin_cookie
    : actor === 'owner'
      ? state.donation_context.owner.cookie
      : state.donation_context.stewards[actor].cookie;
  const context = await browser.newContext({
    viewport: { width: 1440, height: 1000 },
    reducedMotion: 'reduce',
  });
  const origins = new Set([state.admin_url, state.user_url]);
  await context.route('**/*', (route) =>
    origins.has(new URL(route.request().url()).origin)
      ? route.continue()
      : route.abort('blockedbyclient'),
  );
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
    localStorage.setItem('nb.theme', 'light');
  });
  return context;
}

async function read<T>(context: BrowserContext, origin: string, path: string): Promise<T> {
  const response = await context.request.get(origin + path, { headers: { Origin: origin } });
  expect(response.status()).toBe(200);
  return response.json() as Promise<T>;
}
function assertSource(source: Source, sample: Sample) {
  expect(source.base_url).toBe(sample.base_url);
  expect(source.category).toBe(sample.category);
  expect(source.endpoint_tags).toEqual(sample.tags);
}

for (const actor of ['admin', 'five', 'six'] as const) {
  test(`${actor} enters donation review with source classifications and current labels`, async ({
    browser,
  }, info) => {
    const state = fixture();
    const context = await session(browser, state, actor);
    const origin = actor === 'admin' ? state.admin_url : state.user_url;
    const api = actor === 'admin' ? '/admin/api/donations' : '/api/steward/donations';
    const page = await context.newPage();
    const errors: string[] = [];
    page.on('pageerror', (error) => errors.push(error.message));
    try {
      for (const sample of state.donation_context.donations) {
        const query = new URLSearchParams({
          charity_section: 'donations',
          donation_status: 'pending',
          q: sample.description,
        });
        if (actor !== 'admin') query.set('tab', 'charity');
        const [list] = await Promise.all([
          page.waitForResponse((response) => {
            const url = new URL(response.url());
            return (
              response.request().method() === 'GET' &&
              url.pathname === api &&
              url.searchParams.get('q') === sample.description
            );
          }),
          page.goto(origin + (actor === 'admin' ? '/charity?' : '/steward?') + query),
        ]);
        expect(list.status()).toBe(200);
        const listed = (await list.json()) as { data: { id: string; sources: Source[] }[] };
        const summary = listed.data.find((item) => item.id === sample.donation_id);
        expect(summary).toBeDefined();
        assertSource(summary!.sources[0], sample);
        const row = page.getByRole('row').filter({ hasText: sample.description });
        await expect(row).toBeVisible();
        const [received] = await Promise.all([
          page.waitForResponse(
            (response) =>
              response.request().method() === 'GET' &&
              new URL(response.url()).pathname === api + '/' + sample.donation_id,
          ),
          row.getByRole('button', { name: 'Review', exact: true }).click(),
        ]);
        expect(received.status()).toBe(200);
        const detail = (await received.json()) as Detail;
        assertSource(detail.keys[0].safe_source, sample);
        if (actor === 'five') expect(detail.owner).toBeNull();
        await expect(page).toHaveURL(new RegExp('donation_id=' + sample.donation_id));
        await expect(
          page.getByRole('heading', { name: copy.common.donationReview.title, exact: true }),
        ).toBeVisible();
        const keys = await read<{ data: { safe_source: Source }[] }>(
          context,
          origin,
          api + '/' + sample.donation_id + '/keys?page=1&page_size=20',
        );
        assertSource(keys.data[0].safe_source, sample);
        const editor = page.locator('.donation-key-editor').first();
        await expect(editor).toBeVisible();
        const label = (
          sample.category === 'subscription'
            ? copy.common.operations.charity.mainstreamSubscription
            : copy.common.operations.charity.mainstreamApiPlatform
        ).replace('{{name}}', sample.channel_name);
        await expect(editor).toContainText(label);
        const labels = editor.getByLabel(copy.common.operations.charity.currentEndpointTags);
        for (const tag of sample.tags) {
          const text =
            copy.management.endpoints.tag[tag as keyof typeof copy.management.endpoints.tag];
          await expect(labels.getByText(text, { exact: true })).toBeVisible();
        }
        if (sample.category === 'api_platform') {
          await expect(
            labels.getByText(copy.management.endpoints.tag.mysterious, { exact: true }),
          ).toHaveCount(0);
        }
        for (const theme of ['light', 'dark']) {
          await page.evaluate((value) => {
            document.documentElement.dataset.theme = value;
          }, theme);
          for (const width of [1440, 390]) {
            await page.setViewportSize({ width, height: width === 390 ? 844 : 1000 });
            await labels.scrollIntoViewIfNeeded();
            await expect(editor).toContainText(label);
            expect(
              await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
            ).toBe(true);
            const name = `donation-source-${actor}-${sample.category}-${theme}-${width}.png`;
            const target = process.env.NONBIRI_VISUAL_DIR
              ? resolve(process.env.NONBIRI_VISUAL_DIR, name)
              : info.outputPath(name);
            if (process.env.NONBIRI_VISUAL_DIR)
              mkdirSync(process.env.NONBIRI_VISUAL_DIR, { recursive: true });
            await page.screenshot({ path: target, animations: 'disabled' });
          }
        }
      }
      expect(errors).toEqual([]);
    } finally {
      await context.close();
    }
  });
}

test('owner donation APIs keep management source metadata private', async ({ browser }) => {
  const state = fixture();
  const context = await session(browser, state, 'owner');
  try {
    const list = await read<{ data: { id: string }[] }>(
      context,
      state.user_url,
      '/api/donations?view=all&page=1&page_size=100',
    );
    for (const sample of state.donation_context.donations) {
      expect(list.data.some((item) => item.id === sample.donation_id)).toBe(true);
      const detail = await read<Detail>(
        context,
        state.user_url,
        '/api/donations/' + sample.donation_id,
      );
      const keys = await read<unknown>(
        context,
        state.user_url,
        '/api/donations/' + sample.donation_id + '/keys?page=1&page_size=20',
      );
      expect(detail.keys[0].safe_source.base_url).toBe(sample.base_url);
      for (const value of [list, detail, keys]) {
        expect(JSON.stringify(value)).not.toMatch(/"(?:category|channel_revision|endpoint_tags)":/);
      }
    }
  } finally {
    await context.close();
  }
});
