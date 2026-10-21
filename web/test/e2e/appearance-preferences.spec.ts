import { mkdirSync } from 'node:fs';
import { resolve } from 'node:path';
import { expect, test } from './test';
import { USER_ORIGIN } from './ports';
import { appearanceFixtures } from './appearance-fixtures';

for (const mode of ['light', 'dark'])
  for (const width of [1440, 390])
    test(`appearance intensity controls and focus background restore ${mode} ${width}`, async ({
      page,
    }) => {
      await page.setViewportSize({ width, height: 900 });
      await page.addInitScript((mode) => {
        localStorage.setItem('nb.lang', 'zh');
        localStorage.setItem('nb.theme', mode);
      }, mode);
      const media = await page.context().newCDPSession(page);
      const reducedTransparency = (reduce: boolean) =>
        media.send('Emulation.setEmulatedMedia', {
          features: [
            { name: 'prefers-reduced-transparency', value: reduce ? 'reduce' : 'no-preference' },
            { name: 'prefers-reduced-motion', value: 'reduce' },
          ],
        });
      await reducedTransparency(true);
      await appearanceFixtures(page, 'amber');
      await page.goto(USER_ORIGIN + '/account');
      const root = page.locator('html');
      const account = page
        .locator('.core-account-preferences')
        .getByRole('radiogroup', { name: '装扮强度' });
      await expect(account.getByRole('radio', { name: '专注阅读', exact: true })).toBeChecked();
      await expect(root).toHaveAttribute('data-appearance-intensity', 'focus');
      expect(await page.evaluate(() => localStorage.getItem('nb.appearance-intensity'))).toBeNull();
      await reducedTransparency(false);
      await expect(account.getByRole('radio', { name: '均衡', exact: true })).toBeChecked();
      await reducedTransparency(true);
      await expect(root).toHaveAttribute('data-appearance-intensity', 'focus');
      await account.getByText('鲜明', { exact: true }).click();
      await expect(root).toHaveAttribute('data-appearance-intensity', 'vivid');
      if (width === 390) await page.getByRole('button', { name: '打开导航', exact: true }).click();
      await page.getByRole('button', { name: '外观', exact: true }).click();
      const dialog = page.getByRole('alertdialog');
      const controls = dialog.getByRole('radiogroup', { name: '装扮强度' });
      await expect(controls.getByRole('radio', { name: '鲜明', exact: true })).toBeChecked();
      await controls.getByText('专注阅读', { exact: true }).click();
      await expect(account.getByRole('radio', { name: '专注阅读', exact: true })).toBeChecked();
      if (process.env.NONBIRI_VISUAL_DIR) {
        mkdirSync(process.env.NONBIRI_VISUAL_DIR, { recursive: true });
        await page.screenshot({
          path: resolve(process.env.NONBIRI_VISUAL_DIR, `focus-dialog-${mode}-${width}.png`),
        });
      }
      await dialog.getByRole('button', { name: '关闭', exact: true }).click();
      await page.reload();
      await expect(root).toHaveAttribute('data-appearance-intensity', 'focus');
      await expect(account.getByRole('radio', { name: '专注阅读', exact: true })).toBeChecked();
      await reducedTransparency(false);
      await expect(root).toHaveAttribute('data-appearance-intensity', 'focus');
      expect(await page.evaluate(() => localStorage.getItem('nb.appearance-intensity'))).toBe(
        'focus',
      );
      await account.evaluate((element) => element.scrollIntoView({ block: 'center' }));
      const veil = await root.evaluate((element) =>
        getComputedStyle(element).getPropertyValue('--nb-appearance-veil').trim(),
      );
      expect(parseFloat(veil)).toBeGreaterThan(0);
      const trigger = page.getByRole('button', { name: '欣赏背景', exact: true });
      await trigger.click();
      const view = page.getByRole('dialog', { name: '欣赏背景', exact: true });
      await expect(view).toBeVisible();
      expect(
        await root.evaluate((element) =>
          getComputedStyle(element).getPropertyValue('--nb-appearance-veil').trim(),
        ),
      ).toBe('0%');
      expect(
        await page.evaluate(() => {
          const canvas = document.createElement('canvas');
          canvas.width = canvas.height = 1;
          const context = canvas.getContext('2d')!;
          context.fillStyle = getComputedStyle(document.body, '::after').backgroundColor;
          context.fillRect(0, 0, 1, 1);
          return context.getImageData(0, 0, 1, 1).data[3];
        }),
      ).toBe(0);
      if (process.env.NONBIRI_VISUAL_DIR) {
        mkdirSync(process.env.NONBIRI_VISUAL_DIR, { recursive: true });
        await page.screenshot({
          path: resolve(process.env.NONBIRI_VISUAL_DIR, `focus-background-${mode}-${width}.png`),
        });
      }
      await page.keyboard.press('Escape');
      await expect(view).toHaveCount(0);
      await expect(trigger).toBeFocused();
      expect(
        await root.evaluate((element) =>
          getComputedStyle(element).getPropertyValue('--nb-appearance-veil').trim(),
        ),
      ).toBe(veil);
      await expect(account.getByRole('radio', { name: '专注阅读', exact: true })).toBeChecked();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      if (process.env.NONBIRI_VISUAL_DIR)
        await page.screenshot({
          path: resolve(process.env.NONBIRI_VISUAL_DIR, `focus-restored-${mode}-${width}.png`),
        });
      await media.detach();
    });
