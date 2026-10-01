import { test, expect } from './test';
import { mockJson, mockPublicConfig } from './support';
import { USER_ORIGIN } from './ports';

test('a denied login opens the branded public 403 page without starting another login', async ({
  page,
}) => {
  const errors: { type: string; length: number }[] = [];
  page.on('console', (message) => {
    if (message.type() !== 'error' && message.type() !== 'warning') return;
    if (
      message.type() === 'error' &&
      message.text() ===
        'Failed to load resource: the server responded with a status of 401 (Unauthorized)' &&
      message.location().url === USER_ORIGIN + '/api/auth/access-denied-reasons'
    )
      return;
    errors.push({ type: message.type(), length: message.text().length });
  });
  page.on('pageerror', (error) => errors.push({ type: 'pageerror', length: error.message.length }));
  await mockPublicConfig(page, 'user');
  await mockJson(page, {
    origin: USER_ORIGIN,
    method: 'GET',
    path: '/api/auth/access-denied-reasons',
    status: 401,
    body: { error: { code: 'unauthorized', message: 'Synthetic expired explanation grant.' } },
  });
  const authRequests: string[] = [];
  page.on('request', (request) => {
    const url = new URL(request.url());
    if (/^\/api\/(auth(?:\/|$)|session$|me$)/.test(url.pathname)) {
      authRequests.push(request.method() + ' ' + url.pathname + url.search);
    }
  });
  await page.goto(`${USER_ORIGIN}/access-denied`);
  await expect(page.locator('[data-error-kind="403"]')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Access not allowed' })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Back to home' })).toBeVisible();
  await expect(
    page.getByText(
      'Return to the home page and sign in with Discord again to view the current restriction reasons.',
      { exact: true },
    ),
  ).toBeVisible();
  await expect(
    page.getByRole('heading', { name: 'Current access restriction reasons' }),
  ).toHaveCount(0);
  await expect(page.getByText('Synthetic expired explanation grant.', { exact: true })).toHaveCount(
    0,
  );
  expect(authRequests).toEqual(['GET /api/auth/access-denied-reasons']);
  expect(errors).toEqual([]);
});
