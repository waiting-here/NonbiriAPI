import { resolve } from 'node:path';
import type {
  HomeAnnouncementPage,
  HomeAnnouncementSummary,
} from '../../src/user/features/core/types';
import {
  announcementDismissalKey,
  type AnnouncementDetail,
} from '../../src/user/features/operations/data';
import { USER_ORIGIN } from './ports';
import {
  assertNoSensitiveBrowserPersistence,
  collectConsoleViolations,
  installURLPersistenceObserver,
  mockJson,
  mockPublicConfig,
  mockRoleSession,
} from './support';
import { expect, test } from './test';

type BrowserContext = Parameters<typeof installURLPersistenceObserver>[0];
type Page = Parameters<typeof collectConsoleViolations>[0];

type Locale = 'en' | 'zh';
type Theme = 'light' | 'dark';

const EPOCH_A = `b1e_${'E'.repeat(21)}A`;
const EPOCH_B = `b1e_${'F'.repeat(21)}A`;
const CURSOR_ONE = 'YQ';
const EPHEMERAL_MARKER = 'home-browser-ephemeral-marker';

function opaqueID(prefix: 'ann_' | 'b1e_', fill: string): string {
  return `${prefix}${fill.repeat(21)}A`;
}

function summary(
  fill: string,
  overrides: Partial<HomeAnnouncementSummary> = {},
): HomeAnnouncementSummary {
  return {
    epoch: EPOCH_A,
    id: opaqueID('ann_', fill),
    revision: '1',
    severity: 'info',
    pinned: false,
    dismissible: true,
    published_at: 1_800_000_000,
    expires_at: null,
    effective_language: 'en',
    fallback_from: null,
    title: `Announcement ${fill}`,
    excerpt: `Excerpt ${fill}`,
    ...overrides,
  };
}

function detail(value: HomeAnnouncementSummary, rendered_body: string): AnnouncementDetail {
  return {
    epoch: value.epoch,
    id: value.id,
    revision: value.revision,
    severity: value.severity,
    pinned: value.pinned,
    dismissible: value.dismissible,
    published_at: value.published_at,
    expires_at: value.expires_at,
    effective_language: value.effective_language,
    fallback_from: value.fallback_from,
    title: value.title,
    rendered_body,
  };
}

function cursorPage(
  data: HomeAnnouncementSummary[],
  next_cursor: string | null = null,
): HomeAnnouncementPage {
  return { data, next_cursor };
}

function jsonHeaders() {
  return { 'content-type': 'application/json', 'cache-control': 'no-store' };
}

interface HomeAPIState {
  accountId: string;
  allPage?: HomeAnnouncementSummary[];
  homeVariant: 'base' | 'epoch' | 'revision';
  detailAttempts: Map<string, number>;
  detailFor: (
    id: string,
    attempt: number,
    accountId: string,
  ) => { status: number; body?: unknown; delayMs?: number; waitFor?: Promise<void> };
  homeFor: (
    cursor: string | null,
    accountId: string,
    requestNumber: number,
  ) => HomeAnnouncementPage;
  homeFailuresRemaining: number;
  homeRequests: (string | null)[];
  detailRequests: string[];
  activeDetails: number;
  maximumActiveDetails: number;
}

function sessionBody(accountId: string): { user: Record<string, unknown> } {
  const user = {
    id: '1',
    username: 'fixture-user',
    avatar: null,
    avatar_url: null,
    guild_nick: null,
    guild_avatar_url: null,
    lang: 'en',
    is_banned: false,
    banned_until: null,
    charity_suspended_until: null,
    endpoint_limit: null,
    effective_endpoint_limit: '10',
    rpm_limit: null,
    effective_rpm_limit: '60',
    concurrency_limit: null,
    effective_concurrency_limit: '5',
    balance: '0',
    game_balance: '0',
    donation_credit: '0',
    effective_level: 1,
    level_display_name: 'Lv1',
    game_profile_public: false,
    created_at: 1_700_000_000,
    updated_at: 1_700_000_001,
    usage: {
      total_requests: '0',
      total_uncached_input_tokens: '0',
      total_cache_write_input_tokens: '0',
      total_cache_read_input_tokens: '0',
      total_output_tokens: '0',
      total_prompt_tokens: '0',
      total_completion_tokens: '0',
      total_unknown_usage_requests: '0',
    },
  };
  return { user: { ...user, id: accountId } };
}

async function installHomeAPI(page: Page, state: HomeAPIState): Promise<void> {
  await page.route('**/*', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.origin !== USER_ORIGIN || request.method() !== 'GET') {
      await route.fallback();
      return;
    }

    if (url.pathname === '/api/session' || url.pathname === '/api/me') {
      if (state.accountId === '1') {
        await route.fallback();
        return;
      }
      await route.fulfill({
        status: 200,
        headers: jsonHeaders(),
        body: JSON.stringify(sessionBody(state.accountId)),
      });
      return;
    }

    if (url.pathname === '/api/announcements') {
      if (url.searchParams.has('page')) {
        await route.fulfill({
          status: 200,
          headers: jsonHeaders(),
          body: JSON.stringify({
            data: state.allPage ?? [],
            next_cursor: null,
            pagination: {
              page: url.searchParams.get('page'),
              page_size: Number(url.searchParams.get('page_size') ?? '20'),
              total_items: String(state.allPage?.length ?? 0),
              total_pages: '1',
            },
          }),
        });
        return;
      }

      const cursor = url.searchParams.get('cursor');
      state.homeRequests.push(cursor);
      if (state.homeFailuresRemaining > 0) {
        state.homeFailuresRemaining -= 1;
        await route.fulfill({
          status: 200,
          headers: jsonHeaders(),
          body: JSON.stringify({ data: [], next_cursor: 42 }),
        });
        return;
      }
      await route.fulfill({
        status: 200,
        headers: jsonHeaders(),
        body: JSON.stringify(state.homeFor(cursor, state.accountId, state.homeRequests.length)),
      });
      return;
    }

    if (url.pathname.startsWith('/api/announcements/')) {
      const id = decodeURIComponent(url.pathname.slice('/api/announcements/'.length));
      const attempt = (state.detailAttempts.get(id) ?? 0) + 1;
      state.detailAttempts.set(id, attempt);
      state.detailRequests.push(id);
      const response = state.detailFor(id, attempt, state.accountId);
      state.activeDetails += 1;
      state.maximumActiveDetails = Math.max(state.maximumActiveDetails, state.activeDetails);
      try {
        if (response.waitFor) await response.waitFor;
        if (response.delayMs) await new Promise((resolve) => setTimeout(resolve, response.delayMs));
        await route.fulfill({
          status: response.status,
          headers: jsonHeaders(),
          body: JSON.stringify(response.body ?? { error: { code: 'internal' } }),
        });
      } catch {
        // A route can be aborted when the user leaves Home while a detail is pending.
      } finally {
        state.activeDetails -= 1;
      }
      return;
    }

    await route.fallback();
  });
}

async function prepare(
  context: BrowserContext,
  page: Page,
  locale: Locale,
  theme: Theme,
  width: number,
) {
  const consoleGuard = collectConsoleViolations(page);
  await installURLPersistenceObserver(context, [EPHEMERAL_MARKER]);
  await page.setViewportSize({ width, height: 900 });
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.addInitScript(
    ({ selectedLocale, selectedTheme }) => {
      localStorage.setItem('nb.lang', selectedLocale);
      localStorage.setItem('nb.theme', selectedTheme);
    },
    { selectedLocale: locale, selectedTheme: theme },
  );
  await mockPublicConfig(page, 'user', { announcement_epoch: EPOCH_A });
  await mockRoleSession(page, 'user', 'user');
  await mockJson(page, {
    origin: USER_ORIGIN,
    method: 'GET',
    path: '/api/checkin',
    body: { enabled: false },
  });
  await mockJson(page, {
    origin: USER_ORIGIN,
    method: 'GET',
    path: '/api/checkin/game',
    body: { enabled: false },
  });
  await mockJson(page, {
    origin: USER_ORIGIN,
    method: 'GET',
    path: '/api/home/game-summary',
    body: { continue: [], pending_results: [] },
  });
  return consoleGuard;
}

function basicState(
  homeFor: HomeAPIState['homeFor'],
  detailFor: HomeAPIState['detailFor'],
): HomeAPIState {
  return {
    accountId: '1',
    homeVariant: 'base',
    detailAttempts: new Map(),
    detailFor,
    homeFor,
    homeFailuresRemaining: 0,
    homeRequests: [],
    detailRequests: [],
    activeDetails: 0,
    maximumActiveDetails: 0,
  };
}

async function assertHomeLayout(
  page: Page,
  consoleGuard: ReturnType<typeof collectConsoleViolations>,
) {
  const layout = await page.evaluate(() => ({
    innerWidth: window.innerWidth,
    scrollWidth: document.documentElement.scrollWidth,
    overflowing: [...document.querySelectorAll('body *')]
      .filter((element) => {
        const rect = element.getBoundingClientRect();
        return rect.width > 0 && rect.right > window.innerWidth + 1;
      })
      .slice(-10)
      .map((element) => ({
        tag: element.tagName,
        className: typeof element.className === 'string' ? element.className : '',
        text: element.textContent?.slice(0, 80) ?? '',
        right: element.getBoundingClientRect().right,
      })),
  }));
  expect(layout.scrollWidth, `Home overflow: ${JSON.stringify(layout)}`).toBeLessThanOrEqual(
    layout.innerWidth + 1,
  );
  const previews = await page.locator('.home-announcement-preview').evaluateAll((elements) =>
    elements.map((element) => {
      const style = getComputedStyle(element);
      return {
        maxBlockSize: Number.parseFloat(style.maxBlockSize),
        clientHeight: element.clientHeight,
        scrollHeight: element.scrollHeight,
      };
    }),
  );
  for (const preview of previews) {
    expect(preview.maxBlockSize).toBeLessThanOrEqual(120.1);
  }
  await assertNoSensitiveBrowserPersistence(page, [EPHEMERAL_MARKER]);
  consoleGuard.assertNone();
}

async function saveEvidence(page: Page, name: string): Promise<void> {
  const directory = process.env.NONBIRI_VISUAL_DIR;
  const outputPath = directory ? resolve(directory, name) : test.info().outputPath(name);
  await page.screenshot({ path: outputPath, fullPage: true });
  await test.info().attach(name, { path: outputPath, contentType: 'image/png' });
}

test('Home selects the first three visible announcements across cursor pages and renders safe details', async ({
  context,
  page,
}) => {
  const consoleGuard = await prepare(context, page, 'en', 'light', 1_280);
  const hidden = summary('H', { title: 'Hidden announcement', excerpt: 'Hidden excerpt' });
  const first = summary('A', {
    title: 'Pinned release notes with a deliberately long title for wrapping',
    excerpt: 'First excerpt',
    pinned: true,
  });
  const second = summary('B', {
    title: 'Warning announcement',
    excerpt: 'Second excerpt',
    severity: 'warning',
  });
  const third = summary('C', {
    title: 'Persistent important announcement',
    excerpt: 'Third excerpt',
    severity: 'important',
    dismissible: false,
  });
  const fourth = summary('D', { title: 'Later announcement' });
  const byID = new Map([hidden, first, second, third, fourth].map((item) => [item.id, item]));
  const state = basicState(
    (cursor) =>
      cursor === null
        ? cursorPage([hidden, first, second], CURSOR_ONE)
        : cursorPage([third, fourth]),
    (id) => {
      const selected = byID.get(id);
      if (!selected) return { status: 404 };
      return {
        status: 200,
        delayMs: 20,
        body: detail(
          selected,
          id === first.id
            ? '<h2>Release notes</h2><p><strong>Safe heading and paragraph.</strong></p><ul><li>First item</li><li>Second item</li></ul><pre><code>curl https://example.test</code></pre><p><a href="https://docs.example.test/notice" rel="noopener noreferrer">Documentation</a></p>'
            : id === second.id
              ? '<h2>Warning details</h2><p>Review the schedule.</p>'
              : '<h2>Persistent details</h2><p>Important details remain available.</p>',
        ),
      };
    },
  );
  state.allPage = [hidden, first, second, third, fourth];
  await page.addInitScript(
    (key) => localStorage.setItem(key, '1'),
    announcementDismissalKey('1', hidden),
  );
  await installHomeAPI(page, state);

  await page.goto(`${USER_ORIGIN}/`);
  const section = page.locator('.home-announcements');
  await expect(section).toBeVisible();
  await expect(section.locator('.home-announcement-card')).toHaveCount(3);
  await expect(section).not.toContainText(hidden.title);
  await expect(section).toContainText(first.title);
  await expect(section).toContainText(second.title);
  await expect(section).toContainText(third.title);
  await expect(section.locator('a[href="/announcements"]')).toHaveCount(1);
  await expect(section.getByRole('link', { name: 'View all announcements' })).toBeVisible();
  await expect(section.getByRole('link', { name: 'View full announcement' })).toHaveCount(3);
  await expect(section.getByRole('heading', { name: 'Release notes', exact: true })).toBeVisible();
  await expect(section.getByRole('link', { name: 'Documentation' })).toHaveAttribute(
    'href',
    'https://docs.example.test/notice',
  );
  await expect(section.locator('.ops-announcement-body script')).toHaveCount(0);
  await expect(section.locator('a a')).toHaveCount(0);
  expect(state.homeRequests).toEqual([null, CURSOR_ONE]);
  expect(new Set(state.detailRequests)).toEqual(new Set([first.id, second.id, third.id]));
  expect(state.maximumActiveDetails).toBeLessThanOrEqual(3);
  expect(
    await section.evaluate((element) =>
      element.nextElementSibling?.classList.contains('core-grid--wide'),
    ),
  ).toBe(true);
  const previewSizes = await section
    .locator('.home-announcement-preview')
    .evaluateAll((elements) =>
      elements.map((element) => ({ client: element.clientHeight, scroll: element.scrollHeight })),
    );
  expect(previewSizes.some((size) => size.scroll > size.client)).toBe(true);
  await saveEvidence(page, 'home-en-light-desktop.png');
  await assertHomeLayout(page, consoleGuard);
});

test('Home keeps long English previews usable at 320px in light mode', async ({
  context,
  page,
}) => {
  const consoleGuard = await prepare(context, page, 'en', 'light', 320);
  const items = ['A', 'B', 'C'].map((fill, index) =>
    summary(fill, {
      title: `A long English announcement title ${'that wraps safely '.repeat(4)}${index}`,
      excerpt: `A long excerpt ${'with bounded words '.repeat(10)}`,
      severity: index === 1 ? 'warning' : index === 2 ? 'important' : 'info',
    }),
  );
  const state = basicState(
    () => cursorPage(items),
    (id) => {
      const item = items.find((candidate) => candidate.id === id)!;
      return {
        status: 200,
        body: detail(item, `<h2>Long body</h2><p>${'Long paragraph content. '.repeat(30)}</p>`),
      };
    },
  );
  await installHomeAPI(page, state);
  await page.goto(`${USER_ORIGIN}/`);
  const section = page.locator('.home-announcements');
  await expect(section.locator('.home-announcement-card')).toHaveCount(3);
  await expect(section.getByRole('link', { name: 'View all announcements' })).toBeVisible();
  await expect(page.locator('html')).toHaveAttribute('lang', 'en');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
  await saveEvidence(page, 'home-en-light-320.png');
  await assertHomeLayout(page, consoleGuard);
});

test('Home preserves Chinese copy and dark theme at 390px', async ({ context, page }) => {
  const consoleGuard = await prepare(context, page, 'zh', 'dark', 390);
  const items = [
    summary('J', {
      title: `中文公告标题${'用于换行 '.repeat(16)}`,
      excerpt: `中文摘要${'内容 '.repeat(60)}`,
      effective_language: 'zh',
    }),
    summary('K', {
      title: '中文警告公告',
      excerpt: '请查看这条警告。',
      severity: 'warning',
      effective_language: 'zh',
    }),
    summary('L', {
      title: '中文重要公告',
      excerpt: '这条公告不会从首页关闭。',
      severity: 'important',
      dismissible: false,
      effective_language: 'zh',
    }),
  ];
  const state = basicState(
    () => cursorPage(items),
    (id) => {
      const item = items.find((candidate) => candidate.id === id)!;
      return {
        status: 200,
        body: detail(
          item,
          '<h2>安全正文</h2><p><strong>只渲染允许的内容。</strong></p><ul><li>第一项</li><li>第二项</li></ul>',
        ),
      };
    },
  );
  await installHomeAPI(page, state);
  await page.goto(`${USER_ORIGIN}/`);
  const section = page.locator('.home-announcements');
  await expect(section.getByRole('heading', { name: '公告', exact: true })).toBeVisible();
  await expect(section).toContainText('信息');
  await expect(section).toContainText('警告');
  await expect(section).toContainText('重要');
  await expect(section.getByRole('link', { name: '查看全部公告' })).toBeVisible();
  await expect(section.getByRole('button', { name: '在首页隐藏' })).toHaveCount(2);
  await expect(page.locator('html')).toHaveAttribute('lang', 'zh-CN');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await saveEvidence(page, 'home-zh-dark-390.png');
  await assertHomeLayout(page, consoleGuard);
});

test('Home exposes a reachable Retry when the summary list fails', async ({ context, page }) => {
  const consoleGuard = await prepare(context, page, 'en', 'light', 1_280);
  const item = summary('M', { title: 'Retryable list notice' });
  const state = basicState(
    () => cursorPage([item]),
    () => ({ status: 200, body: detail(item, '<h2>Recovered list body</h2><p>Recovered.</p>') }),
  );
  state.homeFailuresRemaining = 1;
  await installHomeAPI(page, state);
  await page.goto(`${USER_ORIGIN}/`);
  const section = page.locator('.home-announcements');
  await expect(section.getByRole('alert')).toContainText('Could not load this section');
  const retry = section.getByRole('button', { name: 'Retry' });
  await expect(retry).toBeVisible();
  await retry.click();
  await expect(section).toContainText(item.title);
  expect(state.homeRequests).toEqual([null, null]);
  await assertHomeLayout(page, consoleGuard);
});

test('Home retains a summary while detail Retry recovers and rejects hostile HTML safely', async ({
  context,
  page,
}) => {
  const consoleGuard = await prepare(context, page, 'en', 'light', 1_280);
  const retryItem = summary('N', { title: 'Detail retry notice' });
  const hostileItem = summary('O', { title: 'Hostile body notice', severity: 'warning' });
  const state = basicState(
    () => cursorPage([retryItem, hostileItem]),
    (id, attempt) => {
      const item = id === retryItem.id ? retryItem : hostileItem;
      if (id === retryItem.id && attempt === 1) {
        return {
          status: 200,
          body: detail({ ...retryItem, revision: '2' }, '<h2>Stale revision</h2>'),
        };
      }
      if (id === hostileItem.id) {
        return {
          status: 200,
          body: detail(
            item,
            '<p>safe prefix</p><script>window.__homeUnexpected = true</script><p>unsafe markup is discarded</p>',
          ),
        };
      }
      return {
        status: 200,
        body: detail(item, '<h2>Recovered detail body</h2><p>Safe retry.</p>'),
      };
    },
  );
  await installHomeAPI(page, state);
  await page.goto(`${USER_ORIGIN}/`);
  const retryCard = page.locator('.home-announcement-card').filter({ hasText: retryItem.title });
  await expect(retryCard).toContainText(retryItem.excerpt);
  await expect(retryCard).toContainText('The full announcement could not be loaded.');
  const retry = retryCard.getByRole('button', { name: 'Retry' });
  await expect(retry).toBeVisible();
  const retryBox = await retry.boundingBox();
  expect(retryBox).not.toBeNull();
  if (retryBox) expect(retryBox.y + retryBox.height).toBeLessThanOrEqual(900 + 1);
  await retry.click();
  await expect(retryCard.getByRole('heading', { name: 'Recovered detail body' })).toBeVisible();
  await expect(
    page.locator('.home-announcement-card').filter({ hasText: hostileItem.title }),
  ).toContainText('The announcement body could not be rendered safely.');
  await expect(page.locator('.ops-announcement-body script')).toHaveCount(0);
  expect(
    await page.evaluate(() => (window as Window & { __homeUnexpected?: boolean }).__homeUnexpected),
  ).toBe(undefined);
  await assertHomeLayout(page, consoleGuard);
});

test('Home refreshes the summary when detail revision changes and shows the new body', async ({
  context,
  page,
}) => {
  const consoleGuard = await prepare(context, page, 'en', 'light', 1_280);
  const oldItem = summary('P', { title: 'Old revision notice', excerpt: 'Old excerpt' });
  const newItem = summary('P', {
    title: 'New revision notice',
    excerpt: 'New excerpt',
    revision: '2',
  });
  const state = basicState(
    (cursor, accountId, requestNumber) => {
      void cursor;
      void accountId;
      return cursorPage([requestNumber === 1 ? oldItem : newItem]);
    },
    (id, attempt) => {
      if (id !== oldItem.id) return { status: 404 };
      return {
        status: 200,
        body: detail(
          attempt === 1 ? { ...oldItem, revision: '2' } : newItem,
          attempt === 1 ? '<h2>Stale body</h2>' : '<h2>Revised body</h2><p>Current revision.</p>',
        ),
      };
    },
  );
  await installHomeAPI(page, state);
  await page.goto(`${USER_ORIGIN}/`);
  const card = page.locator('.home-announcement-card').filter({ hasText: oldItem.title });
  await expect(card.getByRole('button', { name: 'Retry' })).toBeVisible();
  await card.getByRole('button', { name: 'Retry' }).click();
  await expect(
    page.locator('.home-announcement-card').filter({ hasText: newItem.title }),
  ).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Revised body' })).toBeVisible();
  await expect(
    page.locator('.home-announcement-card').filter({ hasText: oldItem.title }),
  ).toHaveCount(0);
  expect(state.detailRequests).toEqual([oldItem.id, newItem.id]);
  await assertHomeLayout(page, consoleGuard);
});

test('Home dismissal persists, restores from the full list, and isolates epoch, revision, and account', async ({
  context,
  page,
}) => {
  const consoleGuard = await prepare(context, page, 'en', 'light', 1_280);
  const dismissible = summary('Q', { title: 'Dismissible home notice' });
  const persistent = summary('R', { title: 'Persistent home notice', dismissible: false });
  const second = summary('S', { title: 'Second home notice' });
  const fourth = summary('T', { title: 'Fourth home notice' });
  const epochItem = summary('Q', { title: 'New epoch notice', epoch: EPOCH_B });
  const revisionItem = summary('Q', {
    title: 'New revision notice',
    epoch: EPOCH_B,
    revision: '2',
  });
  const accountItem = summary('Q', { title: 'Account two notice' });
  const state = basicState(
    (cursor, accountId) => {
      if (accountId === '2') return cursorPage([accountItem]);
      if (state.homeVariant === 'revision' && cursor === null) return cursorPage([revisionItem]);
      if (state.homeVariant === 'epoch' && cursor === null) return cursorPage([epochItem]);
      return cursor === null
        ? cursorPage([dismissible, persistent, second], CURSOR_ONE)
        : cursorPage([fourth]);
    },
    (id, attempt, accountId) => {
      void attempt;
      const candidates =
        accountId === '2'
          ? [accountItem]
          : state.homeVariant === 'epoch'
            ? [epochItem]
            : state.homeVariant === 'revision'
              ? [revisionItem]
              : [dismissible, persistent, second, fourth];
      const item = candidates.find((candidate) => candidate.id === id) ?? candidates[0];
      return { status: 200, body: detail(item, `<h2>${item.title} body</h2><p>Safe content.</p>`) };
    },
  );
  state.allPage = [dismissible, persistent, second, fourth];
  await installHomeAPI(page, state);

  await page.goto(`${USER_ORIGIN}/`);
  const section = page.locator('.home-announcements');
  await section
    .locator('.home-announcement-card')
    .filter({ hasText: dismissible.title })
    .getByRole('button', {
      name: 'Hide from home',
    })
    .click();
  await expect(section).not.toContainText(dismissible.title);
  await expect(section).toContainText(fourth.title);
  await page.reload();
  await expect(page.locator('.home-announcements')).not.toContainText(dismissible.title);
  await expect(page.locator('.home-announcements')).toContainText(fourth.title);

  await page.getByRole('link', { name: 'View all announcements' }).click();
  await expect(page.getByRole('heading', { name: 'Announcements', exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Restore home summary' }).click();
  await expect(page.getByRole('button', { name: 'Restore home summary' })).toHaveCount(0);
  await page.locator('a[href="/"]').first().click();
  await expect(page.locator('.home-announcements')).toContainText(dismissible.title);

  await page.evaluate(
    (key) => localStorage.setItem(key, '1'),
    announcementDismissalKey('1', dismissible),
  );
  state.homeVariant = 'epoch';
  await page.reload();
  await expect(page.locator('.home-announcements')).toContainText(epochItem.title);
  await expect(page.getByRole('heading', { name: 'New epoch notice body' })).toBeVisible();
  await expect(page.locator('.home-announcements')).not.toContainText(
    'The full announcement could not be loaded.',
  );
  await page.evaluate(
    (key) => localStorage.setItem(key, '1'),
    announcementDismissalKey('1', epochItem),
  );
  state.homeVariant = 'revision';
  await page.reload();
  await expect(page.locator('.home-announcements')).toContainText(revisionItem.title);
  await expect(page.getByRole('heading', { name: 'New revision notice body' })).toBeVisible();
  await expect(page.locator('.home-announcements')).not.toContainText(
    'The full announcement could not be loaded.',
  );

  state.accountId = '2';
  await page.reload();
  await expect(page.locator('.home-announcements')).toContainText(accountItem.title);
  await expect(page.locator('.home-announcements')).not.toContainText(revisionItem.title);
  await assertHomeLayout(page, consoleGuard);
});

test('Leaving Home while a detail is pending does not display the late body', async ({
  context,
  page,
}) => {
  const consoleGuard = await prepare(context, page, 'en', 'light', 1_280);
  const item = summary('U', { title: 'Pending detail notice' });
  let release!: () => void;
  const pending = new Promise<void>((resolve) => {
    release = resolve;
  });
  const state = basicState(
    () => cursorPage([item]),
    () => ({
      status: 200,
      body: detail(item, '<h2>Late body must stay hidden</h2>'),
      waitFor: pending,
    }),
  );
  await installHomeAPI(page, state);
  await page.goto(`${USER_ORIGIN}/`);
  await expect(page.getByText('Loading the full announcement…')).toBeVisible();
  await page.getByRole('link', { name: 'View all announcements' }).click();
  await expect(page).toHaveURL(`${USER_ORIGIN}/announcements`);
  await expect(page.getByRole('heading', { name: 'Announcements', exact: true })).toBeVisible();
  release();
  await pending;
  await expect(page.locator('body')).not.toContainText('Late body must stay hidden');
  await assertHomeLayout(page, consoleGuard);
});
