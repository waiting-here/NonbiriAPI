import { mkdirSync, readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { expect, test, type Page } from '@playwright/test';
import enAdmin from '../../src/admin/i18n/en.json' with { type: 'json' };
import zhAdmin from '../../src/admin/i18n/zh.json' with { type: 'json' };
import en from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import zh from '../../src/shared/i18n/common/zh.json' with { type: 'json' };

type Cookie = { Name: string; Value: string };
interface Fixture {
  admin_url: string;
  user_url: string;
  admin_cookie: Cookie;
  users: { level: number; cookie: Cookie }[];
  failure_trace: {
    request_id: string;
    alert_id: string;
    attempt_seq: string;
    health_request_id: string;
    overload_retry_at: string;
  };
}

test.skip(
  process.env.NONBIRI_REQUEST_REJECTIONS_BROWSER !== '1',
  'Requires the real request log fixture.',
);

for (const [language, width, theme] of [
  ['en', 1440, 'light'],
  ['zh', 1440, 'dark'],
  ['zh', 390, 'light'],
  ['en', 390, 'dark'],
] as const) {
  test(`triggering attempt and request-wide failure summary (${language}, ${width}, ${theme})`, async ({
    browser,
  }) => {
    const statePath = process.env.NONBIRI_AUDIT_BROWSER_STATE!;
    const fixture = JSON.parse(readFileSync(statePath, 'utf8')) as Fixture;
    const {
      request_id: requestID,
      alert_id: alertID,
      attempt_seq: attempt,
    } = fixture.failure_trace;
    const common = (language === 'zh' ? zh : en).common;
    const labels = common.operations.logs;
    const alertCopy = (language === 'zh' ? zhAdmin : enAdmin).admin.alerts;
    const alert = alertCopy.triggerAttempt;
    const reasons = common.failure.reason;
    const streak = labels.failureStreak
      .replace('{{before}}', '9')
      .replace('{{after}}', '10')
      .replace('{{threshold}}', '10');
    const visual =
      process.env.NONBIRI_VISUAL_DIR ?? resolve(dirname(statePath), 'failure-trace-preview');
    mkdirSync(visual, { recursive: true });
    const capture = (page: Page, name: string) =>
      page.screenshot({ path: resolve(visual, `failure-trace-${name}-${width}-${theme}.png`) });

    const resultBanner = (page: Page) =>
      page.locator('.nb-expandable-panel:not([hidden])').getByRole('alert').first();

    const expectSummary = async (page: Page) => {
      const summary = page.getByRole('region', { name: labels.failureSummary });
      await expect(summary).toBeVisible();
      await expect(resultBanner(page)).not.toContainText(labels.presentation.retryHint);
      for (const [reason, status, count] of [
        [reasons.google_access_token_type_unsupported, 401, '8'],
        [reasons.google_daily_requests, 429, '5'],
        [reasons.google_api_key_invalid, 400, '1'],
      ] as const) {
        const group = summary.locator('dl > div').filter({ hasText: reason });
        await expect(group.locator('dt')).toHaveText(`${reason} · HTTP ${status}`);
        const label =
          count === '1' ? labels.failureSummaryCount_one : labels.failureSummaryCount_other;
        await expect(group.locator('dd')).toHaveText(label.replace('{{amount}}', count));
      }
      await expect(summary.locator('dl > div')).toHaveCount(3);
      return summary;
    };

    // This fixture has 14 retained attempts. Gaps in the sequence put the
    // triggering sequence (23) in position 12 and on page 2 at 10 per page.
    const expectExactAttempt = async (page: Page) => {
      const target = page.locator('.log-attempt[aria-current="true"]');
      const heading = target.getByRole('heading', {
        name: labels.attemptNumber.replace('{{number}}', attempt),
        exact: true,
      });
      await expect(heading).toBeFocused();
      await expect
        .poll(() =>
          heading.evaluate((element) => {
            const header = document.querySelector('header.site-header')?.getBoundingClientRect();
            const title = element.getBoundingClientRect();
            return (
              header !== undefined &&
              title.top >= header.bottom + 8 &&
              title.bottom <= innerHeight &&
              element.contains(document.elementFromPoint(title.left + 1, title.top + 1)) &&
              element.contains(
                document.elementFromPoint(
                  title.left + title.width / 2,
                  title.top + title.height / 2,
                ),
              )
            );
          }),
        )
        .toBe(true);
      await expect(target.getByText(labels.failureDisablement, { exact: true })).toBeVisible();
      await expect(target.getByText(streak, { exact: true })).toBeVisible();
      await expect(
        target.getByText(reasons.google_access_token_type_unsupported, { exact: true }),
      ).toBeVisible();
      await expect(
        page.locator('.log-attempts + .page-pagination [aria-current="page"]'),
      ).toHaveText('2');
      await expect(page.locator('.log-attempts > li')).toHaveCount(4);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
    };

    for (const role of ['admin', 'steward', 'user'] as const) {
      if (role === 'user' && (width !== 1440 || theme !== 'light')) continue;
      const origin = role === 'admin' ? fixture.admin_url : fixture.user_url;
      const cookie =
        role === 'admin'
          ? fixture.admin_cookie
          : role === 'steward'
            ? fixture.users.find((user) => user.level === 5)!.cookie
            : fixture.users[0].cookie;
      const context = await browser.newContext({
        viewport: { width, height: width === 390 ? 844 : 940 },
        reducedMotion: 'reduce',
        locale: language === 'zh' ? 'zh-CN' : 'en-US',
      });
      try {
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
        await context.addInitScript(
          ({ language, theme, station }) => {
            localStorage.setItem('nb.lang', language);
            localStorage.setItem('nb.theme', theme);
            localStorage.setItem(`nonbiri:${station}:log-attempts-page-size:v1`, '10');
          },
          { language, theme, station: role === 'admin' ? 'admin' : 'user' },
        );
        const page = await context.newPage();
        const targetQuery = new URLSearchParams({ request_id: requestID, attempt_seq: attempt });
        const alertPath = `/alerts?resolved=false&kind=donation_failure_disabled&page=1&page_size=10&alert_id=${alertID}`;

        if (role === 'user') {
          const response = page.waitForResponse(
            (response) => new URL(response.url()).pathname === `/api/logs/${requestID}`,
          );
          await page.goto(origin + '/logs?' + targetQuery);
          const detailResponse = await response;
          expect(detailResponse.ok()).toBe(true);
          expect(new URL(detailResponse.url()).searchParams.has('attempt_seq')).toBe(false);
          const detail = await detailResponse.json();
          expect(detail).not.toHaveProperty('failure_summary');
          expect(detail).not.toHaveProperty('attempts');
          expect(JSON.stringify(detail)).not.toContain('health_impact');
          await expect(page.locator('.nb-expandable-panel:not([hidden])')).toBeVisible();
          await expect(page.getByRole('region', { name: labels.failureSummary })).toHaveCount(0);
          await expect(resultBanner(page)).toContainText(labels.presentation.charityUpstreamHint);
          await expect(page.getByRole('link', { name: alert.view })).toHaveCount(0);
          continue;
        }

        if (role === 'admin') {
          await page.goto(origin + alertPath);
          await expect(
            page.getByText(alertCopy.disablement.service, { exact: true }),
          ).toBeVisible();
          await expect(page.locator('.alert-original-message')).toBeHidden();
          const trigger = page.getByRole('region', { name: alert.title });
          await expect(trigger).toContainText(streak);
          await expect(trigger).toContainText(common.nativeServices.gemini_generate_content);
          await expect(trigger).toContainText(reasons.google_access_token_type_unsupported);
          const viewAttempt = trigger.getByRole('link', { name: alert.view });
          await expect(viewAttempt).toHaveAttribute('href', '/logs?' + targetQuery);
          await expect(viewAttempt).toBeInViewport();
          expect(
            await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth),
          ).toBe(true);
          await capture(page, 'alert');
          await viewAttempt.click();
          await expect(page).toHaveURL(origin + '/logs?' + targetQuery);
        } else {
          await page.goto(origin + '/steward?tab=logs&' + targetQuery);
          await expect(page.getByRole('link', { name: alert.view })).toHaveCount(0);
        }
        await expectExactAttempt(page);
        await capture(page, `${role}-attempt`);
        const summary = await expectSummary(page);
        await summary.scrollIntoViewIfNeeded();
        await capture(page, `${role}-summary`);
        const previous = page
          .locator('.log-attempts + .page-pagination')
          .getByRole('button', { name: common.previous, exact: true });
        await previous.click();
        await expect(page).toHaveURL(
          (url) =>
            url.searchParams.get('attempt_page') === '1' && !url.searchParams.has('attempt_seq'),
        );
        await expect(page.locator('.log-attempts > li')).toHaveCount(10);
        await expectSummary(page);
        await page.goBack();
        await expectExactAttempt(page);

        if (role === 'admin') {
          await page.goBack();
          await expect(page).toHaveURL(origin + alertPath);
          await expect(page.getByRole('link', { name: alert.view })).toBeVisible();
          await page.goForward();
          await expectExactAttempt(page);
        }
        if (role === 'steward' && width === 390 && theme === 'light') {
          await page.goto(origin + `/steward?tab=logs&request_id=${requestID}&attempt_seq=100`);
          const missing = page.getByText(labels.attemptUnavailable, { exact: true });
          await expect(missing).toBeFocused();
          await expect(missing).toBeInViewport();
          await expect(page.locator('.log-attempt[aria-current="true"]')).toHaveCount(0);
          await expectSummary(page);
          await capture(page, 'missing-attempt');
        }

        const healthURL = role === 'admin' ? '/logs?' : '/steward?tab=logs&';
        await page.goto(
          origin + healthURL + 'request_id=' + fixture.failure_trace.health_request_id,
        );
        await page.locator('summary').getByText(labels.attempts, { exact: true }).click();
        const calls = page.locator('.log-attempts > li');
        await expect(calls).toHaveCount(3);
        const impact = labels.healthImpact;
        const overload = calls.nth(0);
        const streakLine = overload.locator('.log-attempt-streak');
        await expect(streakLine).toContainText(impact.streak.unchanged);
        await expect(streakLine).toContainText('5 → 5');
        await expect(streakLine).toContainText(impact.didNotDisable);
        const wait = overload.locator('.log-attempt-backoff');
        await expect(wait).toContainText(impact.backoff.triggered);
        await expect(wait).toContainText(reasons.google_model_overloaded);
        await expect(wait).toContainText(impact.backoff.scope.service);
        await expect(wait).toContainText(common.nativeServices.gemini_generate_content);
        await expect(wait.locator('time')).toHaveAttribute(
          'datetime',
          new Date(Number(fixture.failure_trace.overload_retry_at) * 1000).toISOString(),
        );
        const modelWait = calls.nth(1).locator('.log-attempt-backoff');
        await expect(modelWait).toContainText(reasons.google_daily_requests);
        await expect(modelWait).toContainText(impact.backoff.scope.model);
        await expect(modelWait).toContainText('fixture-flash');
        await expect(calls.nth(2).locator('.log-attempt-streak')).toContainText(
          impact.streak.reset,
        );
        await expect(calls.nth(2).locator('.log-attempt-streak')).toContainText('4 → 0');
        await expect(calls.nth(2).locator('.log-attempt-backoff')).toHaveCount(0);
        await expect(overload.getByText(common.failure.missing, { exact: true })).toHaveCount(0);
        const callHeading = overload.getByRole('heading');
        await callHeading.evaluate((element) => element.scrollIntoView({ block: 'center' }));
        await expect(callHeading).toBeInViewport();
        await expect(streakLine).toBeInViewport();
        await expect(wait.locator('time')).toBeInViewport();
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
          true,
        );
        await capture(page, `${role}-health`);
      } finally {
        await context.close();
      }
    }
  });
}
