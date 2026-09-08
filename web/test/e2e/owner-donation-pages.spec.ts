import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import { expect, test } from './test';
import { USER_ORIGIN } from './ports';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import en from '../../src/user/i18n/en.json' with { type: 'json' };
import zh from '../../src/user/i18n/zh.json' with { type: 'json' };
import commonEn from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import commonZh from '../../src/shared/i18n/common/zh.json' with { type: 'json' };

const NOW = 1_800_000_000;
const source = {
  kind: 'custom',
  connector_type: 'openai-compatible',
  base_url: `https://controlled.example/v1/${'long-source-'.repeat(20)}`,
};
const counts = {
  available: '0',
  pending: '1',
  disabled: '0',
  suspended: '0',
  exhausted: '0',
  expired: '0',
  ended: '0',
};
function summary(index: number) {
  return {
    id: String(index),
    status: 'pending',
    revision: '1',
    description: index === 21 ? 'needle submission' : `Submission ${index}`,
    review_result: null,
    created_at: NOW,
    updated_at: NOW,
    key_count: index === 21 ? '21' : '1',
    state_counts: { ...counts, pending: index === 21 ? '21' : '1' },
    source_count: '1',
    sources: [source],
  };
}
function key(index: number) {
  return {
    id: String(index),
    key_id: String(index),
    donation_id: '21',
    donation_revision: '1',
    endpoint_key_id: String(index + 100),
    display_head: `head${index}`,
    display_tail: 'tail',
    safe_source: source,
    physical_enabled: false,
    charity_state: 'pending',
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
    rule_count: '0',
    rules: [],
  };
}
function pageWindow(rows: unknown[], params: URLSearchParams) {
  const size = Number(params.get('page_size'));
  const total = Math.max(1, Math.ceil(rows.length / size));
  const page = Math.min(Number(params.get('page')), total);
  return {
    data: rows.slice((page - 1) * size, page * size),
    next_cursor: null,
    pagination: {
      page: String(page),
      page_size: size,
      total_items: String(rows.length),
      total_pages: String(total),
    },
  };
}

for (const [width, locale, theme] of [
  [320, 'en', 'light'],
  [390, 'zh', 'dark'],
  [1280, 'en', 'dark'],
] as const) {
  test(`owner donation pages preserve nested selection and return at ${width} ${locale} ${theme}`, async ({
    page,
  }) => {
    const copy = (locale === 'en' ? en : zh).user.charity;
    const common = (locale === 'en' ? commonEn : commonZh).common;
    const errors = collectConsoleViolations(page);
    await page.setViewportSize({ width, height: 900 });
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await page.addInitScript(
      ({ locale, theme }) => {
        localStorage.setItem('nb.lang', locale);
        localStorage.setItem('nb.theme', theme);
      },
      { locale, theme },
    );
    await mockPublicConfig(page, 'user');
    await mockRoleSession(page, 'user', 'user');
    const reads: string[] = [];
    await page.route('**/*', async (route) => {
      const request = route.request();
      const url = new URL(request.url());
      if (url.origin !== USER_ORIGIN) {
        await route.fallback();
        return;
      }
      if (url.pathname === '/api/charity/models') {
        await route.fulfill({
          json: url.searchParams.has('page')
            ? {
                models: [],
                pagination: { page: '1', page_size: 20, total_items: '0', total_pages: '1' },
                donation_intake: 'closed',
                server_now: NOW,
              }
            : { state: 'no_models', models: [], donation_intake: 'closed', server_now: NOW },
        });
        return;
      }
      if (!url.pathname.startsWith('/api/donations')) {
        await route.fallback();
        return;
      }
      expect(request.method()).toBe('GET');
      reads.push(url.pathname + url.search);
      if (url.pathname === '/api/donations/21') {
        const header = Object.fromEntries(
          Object.entries(summary(21)).filter(
            ([name]) => !['key_count', 'state_counts', 'source_count', 'sources'].includes(name),
          ),
        );
        const keys = Array.from({ length: 21 }, (_, index) => {
          return Object.fromEntries(
            Object.entries(key(index + 1)).filter(
              ([name]) =>
                !['key_id', 'donation_id', 'donation_revision', 'rule_count', 'rules'].includes(
                  name,
                ),
            ),
          );
        });
        await route.fulfill({ json: { ...header, keys } });
        return;
      }
      expect(url.searchParams.has('page')).toBe(true);
      expect(url.searchParams.has('cursor')).toBe(false);
      if (url.pathname === '/api/donations') {
        const rows = url.searchParams.get('q')
          ? [summary(21)]
          : Array.from({ length: 21 }, (_, index) => summary(index + 1));
        await route.fulfill({ json: pageWindow(rows, url.searchParams) });
        return;
      }
      if (url.pathname === '/api/donations/21/keys') {
        await route.fulfill({
          json: pageWindow(
            Array.from({ length: 21 }, (_, index) => key(index + 1)),
            url.searchParams,
          ),
        });
        return;
      }
      throw new Error(`Unexpected owner request: ${url.pathname}`);
    });
    await page.goto(`${USER_ORIGIN}/charity?tab=donations`);
    const panel = page.locator('.economy-owner-pages');
    await expect(panel.getByText('Submission 1', { exact: true })).toBeVisible();
    expect(reads.some((path) => path.includes('/keys'))).toBe(false);
    await panel.getByRole('button', { name: common.next, exact: true }).click();
    await expect(panel.getByText('needle submission', { exact: true })).toBeVisible();
    await panel.getByRole('button', { name: copy.ownerPages.keys, exact: true }).click();
    const keys = panel.getByRole('region', { name: copy.ownerPages.keys, exact: true });
    await expect(keys.getByText('head1…tail', { exact: true })).toBeVisible();
    await keys.getByRole('button', { name: common.next, exact: true }).click();
    await expect(keys.getByText('head21…tail', { exact: true })).toBeVisible();
    await panel.getByRole('searchbox').fill('needle');
    await panel.getByRole('button', { name: common.search, exact: true }).click();
    await expect(page).toHaveURL(/donation_q=needle/);
    await expect(keys.getByText('head21…tail', { exact: true })).toBeVisible();
    await page.reload();
    await expect(keys.getByText('head21…tail', { exact: true })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    if (width < 640) {
      for (const navigation of await panel
        .getByRole('navigation', { name: common.pagination })
        .all()) {
        const controls = [
          navigation.getByRole('button', { name: common.previous, exact: true }),
          navigation.getByRole('button', { name: common.next, exact: true }),
          navigation.getByRole('textbox', { name: common.pageControls.jump, exact: true }),
          navigation.getByRole('button', { name: common.pageControls.go, exact: true }),
        ];
        const boxes = await Promise.all(controls.map((control) => control.boundingBox()));
        expect(
          boxes.every((box) => box && box.width > 0 && box.x >= 0 && box.x + box.width <= width),
        ).toBe(true);
        const bottoms = boxes.map((box) => box!.y + box!.height);
        expect(Math.max(...bottoms) - Math.min(...bottoms)).toBeLessThanOrEqual(1);
      }
    }
    if (process.env.NONBIRI_VISUAL_DIR) {
      await mkdir(process.env.NONBIRI_VISUAL_DIR, { recursive: true });
      await page.screenshot({
        path: resolve(
          process.env.NONBIRI_VISUAL_DIR,
          `owner-donations-${width}-${locale}-${theme}.png`,
        ),
        fullPage: true,
      });
    }
    await panel.getByRole('link', { name: copy.openDonationDetail, exact: true }).click();
    await expect(page).toHaveURL(/\/charity\/donations\/21/);
    await page.getByRole('link', { name: copy.backToDonations, exact: true }).click();
    await expect(page).toHaveURL(/donation_q=needle/);
    await expect(keys.getByText('head21…tail', { exact: true })).toBeVisible();
    expect(reads.every((path) => !path.includes('limit='))).toBe(true);
    errors.assertNone();
  });
}
