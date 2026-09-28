import { expect, test } from './test';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';

async function setup(page: import('@playwright/test').Page) {
  await mockRoleSession(page, 'admin', 'admin');
  await mockPublicConfig(page, 'admin');
  await page.emulateMedia({ reducedMotion: 'reduce' });
}
async function layout(
  page: import('@playwright/test').Page,
  name: string,
  multilineNote?: import('@playwright/test').Locator,
) {
  for (const width of [1440, 390, 320]) {
    await page.setViewportSize({ width, height: 1000 });
    if (multilineNote) {
      await expect(multilineNote).toHaveCSS('white-space', 'pre-wrap');
      await expect(multilineNote).toHaveCSS('overflow-wrap', 'anywhere');
      const lineGeometry = await multilineNote.evaluate((element) => {
        const range = document.createRange();
        range.selectNodeContents(element);
        const fragments = Array.from(range.getClientRects());
        const lineHeight = Number.parseFloat(getComputedStyle(element).lineHeight);
        return {
          fragmentCount: fragments.length,
          fragmentHeight: fragments[0]?.height ?? 0,
          height: range.getBoundingClientRect().height,
          lineHeight,
        };
      });
      expect(lineGeometry.fragmentCount).toBeGreaterThanOrEqual(3);
      expect(lineGeometry.height).toBeGreaterThanOrEqual(lineGeometry.lineHeight * 3 - 1);
    }
    await expect
      .poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1))
      .toBe(true);
    expect(
      await page
        .locator('main button')
        .evaluateAll(
          (buttons) => buttons.filter((button) => !button.classList.contains('btn')).length,
        ),
    ).toBe(0);
    expect(
      await page.locator('main form input, main form button').evaluateAll(
        (nodes) =>
          nodes.filter((node) => {
            const rect = node.getBoundingClientRect();
            return rect.width > 0 && (rect.left < 0 || rect.right > innerWidth + 1);
          }).length,
      ),
    ).toBe(0);
    await page.screenshot({ path: `../tmp/${name}-${width}.png`, fullPage: true });
  }
}

test('deletion alerts can be filtered and resolved in a selected batch', async ({ page }) => {
  const errors = collectConsoleViolations(page);
  await setup(page);
  let done = false;
  await page.route('**/admin/api/alerts**', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (request.method() === 'POST') {
      expect(url.pathname).toBe('/admin/api/alerts/resolve');
      expect(request.postDataJSON()).toEqual({ ids: ['7'] });
      done = true;
      await route.fulfill({ json: { resolved_count: 1 } });
      return;
    }
    const kind = url.searchParams.get('kind') ?? 'account_deleted';
    const data = done
      ? []
      : [
          {
            id: '7',
            kind,
            message: 'Review',
            ref: null,
            subject_user_id: null,
            created_at: 1800000000,
            resolved: false,
            resolved_at: null,
            ...(kind === 'account_deleted'
              ? {
                  account_deletion: {
                    user_id: '42',
                    discord_id: '123456789012345678',
                    general_balance: '-2500.125',
                    game_balance: '12',
                    donation_credit: '120',
                    sketch_paper: '1',
                    sketch_brush: '0',
                  },
                }
              : {}),
          },
        ];
    await route.fulfill({
      json: {
        data,
        next_cursor: null,
        pagination: {
          page: '1',
          page_size: 20,
          total_items: String(data.length),
          total_pages: '1',
        },
      },
    });
  });
  await page.goto(`${ADMIN_ORIGIN}/alerts`);
  await page.getByLabel('Alert type').selectOption('account_deleted');
  await expect(page.getByText('123456789012345678')).toBeVisible();
  await expect(page.getByText('-2500.125')).toBeVisible();
  await layout(page, 'deletion-alert');
  await page.getByRole('checkbox', { name: 'Select alert 7' }).check();
  await page.getByRole('button', { name: 'Resolve selected (1)' }).click();
  await expect(page.getByText('123456789012345678')).toHaveCount(0);
  expect(done).toBe(true);
  errors.assertNone();
});

test('blacklist add and remove work on desktop and narrow screens', async ({ page }) => {
  const errors = collectConsoleViolations(page);
  await setup(page);
  const reason = `Policy review\n<img src=x onerror="window.blacklistNoteRan=true">\n${'x'.repeat(160)}`;
  let listed = false;
  await page.route('**/admin/api/blacklist**', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (request.method() === 'POST') {
      expect(request.headers()['idempotency-key']).toMatch(/^[A-Za-z0-9_-]{22}$/);
      listed = !url.pathname.endsWith('/remove');
      if (listed)
        expect(request.postDataJSON()).toEqual({
          discord_id: '123456789012345678',
          reason,
        });
      await route.fulfill({ status: 204 });
      return;
    }
    const data = listed
      ? [
          {
            discord_id: '123456789012345678',
            reason,
            created_at: 1800000000,
            user_id: '42',
          },
        ]
      : [];
    await route.fulfill({
      json: {
        data,
        next_cursor: null,
        pagination: {
          page: '1',
          page_size: 20,
          total_items: String(data.length),
          total_pages: '1',
        },
      },
    });
  });
  await page.goto(`${ADMIN_ORIGIN}/blacklist`);
  await page.getByLabel('Discord ID', { exact: true }).fill('123456789012345678');
  await page.getByLabel('Reason', { exact: true }).fill(reason);
  await page.getByRole('button', { name: 'Add and permanently ban' }).click();
  await expect(page.getByRole('link', { name: '42', exact: true })).toBeVisible();
  const reasonNote = page.locator('.ops-blacklist-note');
  await expect(reasonNote).toBeVisible();
  expect(await reasonNote.textContent()).toBe(reason);
  await expect(reasonNote.locator('img')).toHaveCount(0);
  await layout(page, 'blacklist', reasonNote);
  await page.getByRole('button', { name: 'Remove from blacklist' }).click();
  await expect(page.getByRole('link', { name: '42', exact: true })).toHaveCount(0);
  expect(listed).toBe(false);
  errors.assertNone();
});

test('administrator can inspect an old deleted account without mutation controls', async ({ page }) => {
  const errors = collectConsoleViolations(page);
  await setup(page);
  const discordID = '123456789012345678';
  const deleted = {
    record_id: '7', former_user_id: '42', discord_id: discordID, snapshot_version: 1,
    registered_at: null, deleted_at: 1_800_000_000, effective_level: null,
    ban: { state: 'unknown', active_at_deletion: null, reason: null, until: null },
    charity_pause: { state: 'unknown', active_at_deletion: null, reason: null, until: null },
    source: 'unknown', actor_user_id: null, blacklist_action: 'unknown', blacklist_reason_codes: [],
    general_balance: '-2', game_balance: null, donation_credit: null,
    sketch_paper: null, sketch_brush: null, alert_id: '7',
  };
  const pageBody = (data: unknown[]) => ({ data, next_cursor: null, pagination: {
    page: '1', page_size: 20, total_items: String(data.length), total_pages: '1',
  } });
  await page.route('**/admin/api/users**', async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname === '/admin/api/users') {
      expect(url.searchParams.get('account_state')).toBe('all');
      await route.fulfill({ json: pageBody([{ account_state: 'deleted', deleted }]) });
    } else if (url.pathname === '/admin/api/users/deleted/7') {
      await route.fulfill({ json: deleted });
    } else if (url.pathname === '/admin/api/users/deletion-duel-aborts') {
      expect(url.searchParams.get('discord_id')).toBe(discordID);
      await route.fulfill({ json: pageBody([]) });
    } else {
      await route.fallback();
    }
  });
  await page.goto(`${ADMIN_ORIGIN}/users`);
  await page.getByRole('button', { name: 'View', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Deleted account #7' })).toBeVisible();
  expect(await page.getByText('Unknown', { exact: true }).count()).toBeGreaterThan(0);
  await expect(page.getByText('No retained match cancellations')).toBeVisible();
  await expect(page.getByRole('link', { name: 'All accounts for this Discord ID' })).toHaveAttribute('href', /discord_id=123456789012345678/);
  await expect(page.getByRole('button', { name: /ban|unban|delete|restore/i })).toHaveCount(0);
  errors.assertNone();
});

test('level six can read administrator-origin blacklist events without a removal control', async ({ page }) => {
  const errors = collectConsoleViolations(page);
  await mockRoleSession(page, 'user', 'level6');
  await mockPublicConfig(page, 'user');
  const discordID = '123456789012345678';
  const note = `Account deletion attempt\n<img src=x onerror="window.blacklistNoteRan=true">\n${'y'.repeat(160)}`;
  await page.route('**/api/steward/blacklist**', async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname.endsWith('/events')) {
      await route.fulfill({ json: {
        data: [{ id: '1', actor_kind: 'admin', actor_user_id: '9', reason_codes: [], safe_note: note, created_at: 1_800_000_000 }],
        next_cursor: null,
        pagination: { page: '1', page_size: 20, total_items: '1', total_pages: '1' },
      } });
    } else {
      await route.fulfill({ json: {
        data: [{ discord_id: discordID, reason: note, created_at: 1_800_000_000, user_id: null, first_actor_kind: 'admin', first_actor_user_id: '9' }],
        next_cursor: null,
        pagination: { page: '1', page_size: 20, total_items: '1', total_pages: '1' },
      } });
    }
  });
  await page.goto(`${USER_ORIGIN}/steward?tab=blacklist`);
  await expect(page.getByRole('cell', { name: 'Administrator #9' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Remove from blacklist' })).toHaveCount(0);
  const reasonNote = page.locator('.ops-blacklist-note').first();
  await expect(reasonNote).toBeVisible();
  expect(await reasonNote.textContent()).toBe(note);
  await expect(reasonNote.locator('img')).toHaveCount(0);
  await page.getByRole('button', { name: 'Events' }).click();
  const eventNote = page.locator('.ops-blacklist-note').nth(1);
  await expect(eventNote).toBeVisible();
  await expect(eventNote.locator('..')).toContainText('Additional note:');
  expect(await eventNote.textContent()).toBe(note);
  await expect(eventNote.locator('img')).toHaveCount(0);
  await layout(page, 'steward-blacklist', eventNote);
  errors.assertNone();
});
