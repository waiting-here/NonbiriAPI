import { expect, test } from './test';
import { ADMIN_ORIGIN, USER_ORIGIN } from './ports';
import { mockPublicConfig, mockRoleSession } from './support';

for (const width of [1024, 390]) {
  test(`last-row menu stays visible and usable at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 720 });
    await page.addInitScript(() => localStorage.setItem('nb.lang', 'en'));
    await mockPublicConfig(page, 'admin');
    await mockRoleSession(page, 'admin', 'admin');
    let removed = false;
    let writes = 0;
    await page.route('**/admin/api/blacklist**', async (route) => {
      if (route.request().method() === 'POST') {
        expect(new URL(route.request().url()).pathname).toBe(
          '/admin/api/blacklist/123456789012345678/remove',
        );
        removed = true;
        writes += 1;
        await route.fulfill({ status: 204 });
        return;
      }
      await route.fulfill({
        json: {
          data: removed
            ? []
            : [
                {
                  discord_id: '123456789012345678',
                  reason: 'Synthetic reason',
                  created_at: 1800000000,
                  user_id: '42',
                },
              ],
          next_cursor: null,
          pagination: {
            page: '1',
            page_size: 20,
            total_items: removed ? '0' : '1',
            total_pages: '1',
          },
        },
      });
    });
    await page.goto(ADMIN_ORIGIN + '/blacklist');
    const trigger = page.getByRole('button', { name: /123456789012345678/ });
    await trigger.click();
    const menu = page.getByRole('menu');
    await expect(menu).toBeVisible();
    const remove = page.getByRole('menuitem', { name: 'Remove from blacklist' });
    const visible = await remove.evaluate((element) => {
      const bounds = element.getBoundingClientRect();
      return (
        bounds.left >= 0 &&
        bounds.right <= innerWidth &&
        bounds.top >= 0 &&
        bounds.bottom <= innerHeight &&
        element.contains(
          document.elementFromPoint(bounds.x + bounds.width / 2, bounds.y + bounds.height / 2),
        )
      );
    });
    expect(visible).toBe(true);
    await page.keyboard.press('Escape');
    await expect(trigger).toBeFocused();
    await expect(menu).toBeHidden();
    await trigger.click();
    await remove.click();
    await expect.poll(() => writes).toBe(1);
    await expect(trigger).toHaveCount(0);
  });
}

for (const id of ['invalid', '999']) {
  test(`missing donation ${id} returns to the donations tab`, async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('nb.lang', 'en'));
    await mockPublicConfig(page, 'user');
    await mockRoleSession(page, 'user', 'user');
    await page.route('**/api/donations/999', (route) =>
      route.fulfill({
        status: 404,
        json: { error: { code: 'not_found', message: 'Donation not found.' } },
      }),
    );
    await page.goto(USER_ORIGIN + '/charity/donations/' + id);
    const links = page.getByRole('link', { name: 'Back to charity', exact: true });
    await expect(links).toHaveCount(2);
    for (const link of await links.all())
      await expect(link).toHaveAttribute('href', '/charity?tab=donations');
  });
}
