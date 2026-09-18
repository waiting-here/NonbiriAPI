import { test, expect } from './test';
import { collectConsoleViolations, mockPublicConfig } from './support';
import { USER_ORIGIN } from './ports';

test('a denied login opens the branded public 403 page without starting another login', async ({
  page,
}) => {
  const errors = collectConsoleViolations(page);
  await mockPublicConfig(page, 'user');
  const authRequests: string[] = [];
  page.on('request', (request) => {
    if (request.url().includes('/api/auth/')) authRequests.push(request.url());
  });
  await page.goto(`${USER_ORIGIN}/access-denied`);
  await expect(page.locator('[data-error-kind="403"]')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Access not allowed' })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Back to home' })).toBeVisible();
  expect(authRequests).toEqual([]);
  errors.assertNone();
});
