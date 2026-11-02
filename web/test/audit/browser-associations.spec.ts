import { mkdirSync, readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { expect, test, type BrowserContext } from '@playwright/test';

interface Fixture {
  admin_url: string;
  user_url: string;
  admin_cookie: { Name: string; Value: string };
  users: { id: string; level: number; cookie: { Name: string; Value: string } }[];
}

test.skip(process.env.NONBIRI_MANAGEMENT_BROWSER !== '1', 'Requires the management fixture.');

test('authenticated browser associations remain review-only across accounts and both stations', async ({
  browser,
}) => {
  test.setTimeout(150_000);
  const fixture = JSON.parse(
    readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8'),
  ) as Fixture;
  const first = fixture.users.find((user) => user.level === 1)!;
  const second = fixture.users.find((user) => user.level === 6)!;
  const cookie = async (
    context: BrowserContext,
    origin: string,
    value: { Name: string; Value: string },
    admin = false,
  ) => {
    await context.addCookies([
      {
        name: value.Name,
        value: value.Value,
        domain: new URL(origin).hostname,
        path: admin ? '/admin' : '/api',
        httpOnly: true,
        sameSite: 'Lax',
      },
    ]);
  };
  const shared = await browser.newContext();
  try {
    await cookie(shared, fixture.user_url, first.cookie);
    const session = await shared.request.get(fixture.user_url + '/api/session');
    expect(session.ok()).toBe(true);
    const issued = (await shared.cookies()).find((entry) => entry.name === 'nb_browser_audit');
    expect(issued).toBeDefined();
    expect(issued?.httpOnly).toBe(true);
    expect(issued?.sameSite).toBe('Lax');
    const denied = await shared.request.get(
      fixture.user_url + '/api/steward/abuse-audit/shared-browsers',
    );
    expect(denied.status()).toBe(403);

    await cookie(shared, fixture.user_url, second.cookie);
    expect((await shared.request.get(fixture.user_url + '/api/me')).ok()).toBe(true);
    expect((await shared.cookies()).find((entry) => entry.name === 'nb_browser_audit')?.value).toBe(
      issued?.value,
    );
    const response = await shared.request.get(
      fixture.user_url + '/api/steward/abuse-audit/shared-browsers?page=1&page_size=100',
    );
    expect(response.ok()).toBe(true);
    const data = (await response.json()) as {
      items: { browser_id: string; users: number }[];
    };
    const group = data.items.find((entry) => entry.users === 2);
    expect(group).toBeDefined();
    expect(group!.users).toBe(2);
    const members = await shared.request.get(
      fixture.user_url + '/api/steward/abuse-audit/shared-browsers/' + group!.browser_id,
    );
    expect(members.ok()).toBe(true);
    expect(
      ((await members.json()) as { associations: { user_id: string }[] }).associations.map(
        (entry) => entry.user_id,
      ),
    ).toEqual(expect.arrayContaining([first.id, second.id]));
    expect(JSON.stringify(data)).not.toContain(issued!.value);

    for (const role of ['admin', 'steward'] as const) {
      for (const width of [1440, 390]) {
        for (const theme of ['light', 'dark']) {
          const origin = role === 'admin' ? fixture.admin_url : fixture.user_url;
          const context = await browser.newContext({
            viewport: { width, height: 950 },
            colorScheme: theme === 'dark' ? 'dark' : 'light',
          });
          try {
            await cookie(
              context,
              origin,
              role === 'admin' ? fixture.admin_cookie : second.cookie,
              role === 'admin',
            );
            await context.addInitScript((theme) => {
              localStorage.setItem('nb.lang', 'zh');
              localStorage.setItem('nb.theme', theme);
            }, theme);
            const page = await context.newPage();
            const path = role === 'admin' ? '/risk-audit?' : '/steward?tab=risk&';
            await page.goto(
              origin +
                path +
                'audit_tab=browsers&audit_browser_size=100&audit_browser=' +
                encodeURIComponent(group!.browser_id),
            );
            await expect(
              page.getByRole('heading', { name: '同浏览器多账号', exact: true }),
            ).toBeVisible();
            await expect(
              page.getByRole('link', { name: '用户 ' + first.id, exact: true }),
            ).toBeVisible();
            await expect(
              page.getByRole('link', { name: '用户 ' + second.id, exact: true }),
            ).toBeVisible();
            await expect(page.getByText(/不能据此认定是同一个人/)).toBeVisible();
            expect(
              await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
            ).toBe(true);
            if (process.env.NONBIRI_VISUAL_DIR) {
              mkdirSync(process.env.NONBIRI_VISUAL_DIR, { recursive: true });
              await page.screenshot({
                path: resolve(
                  process.env.NONBIRI_VISUAL_DIR,
                  `browser-${role}-${width}-${theme}.png`,
                ),
                fullPage: true,
              });
            }
            await page.getByRole('tab', { name: '同一身份的多个 IP', exact: true }).click();
            await expect(
              page.getByRole('tab', { name: '同一身份的多个 IP', exact: true }),
            ).toHaveAttribute('aria-selected', 'true');
            await page.getByRole('tab', { name: '同浏览器多账号', exact: true }).click();
            await expect(
              page.getByRole('link', { name: '用户 ' + first.id, exact: true }),
            ).toBeVisible();
          } finally {
            await context.close();
          }
        }
      }
    }
  } finally {
    await shared.close();
  }
});
