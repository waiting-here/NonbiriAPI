import { build } from 'vite';
import react from '@vitejs/plugin-react-swc';
import { resolve } from 'node:path';
import { mkdirSync } from 'node:fs';
import { test, expect } from './test';
import { FIXTURE_ORIGIN } from './ports';

const assets = new Map<string, { body: string | Uint8Array; contentType: string }>();
test.beforeAll(async () => {
  const output = await build({
    configFile: false,
    logLevel: 'error',
    plugins: [react()],
    resolve: { alias: { '@shared': resolve('src/shared') } },
    define: { 'process.env.NODE_ENV': JSON.stringify('production') },
    build: {
      write: false,
      lib: {
        entry: resolve('test/fixtures/aria-controls.tsx'),
        formats: ['es'],
        fileName: () => 'controls.js',
      },
    },
  });
  for (const bundle of Array.isArray(output) ? output : [output]) {
    if (!('output' in bundle)) continue;
    for (const file of bundle.output)
      assets.set(file.fileName, {
        body: file.type === 'chunk' ? file.code : file.source,
        contentType: file.fileName.endsWith('.css') ? 'text/css' : 'application/javascript',
      });
  }
});

for (const width of [1440, 390])
  for (const theme of ['light', 'dark']) {
    test('stable overlay composition ' + width + ' ' + theme, async ({ page }) => {
      const clickVisibleLabel = async (control: ReturnType<typeof page.locator>) => {
        const label = control.locator('xpath=ancestor::label[1]');
        await label.scrollIntoViewIfNeeded();
        await expect(label).toBeVisible();
        const bounds = await label.boundingBox();
        expect(bounds).not.toBeNull();
        // Send a real pointer action; locator.click intentionally waits for enabled controls.
        await page.mouse.click(bounds!.x + bounds!.width / 2, bounds!.y + bounds!.height / 2);
      };
      const violations: string[] = [];
      page.on('pageerror', (error) => violations.push(error.message));
      await page.setViewportSize({ width, height: 900 });
      await page.emulateMedia({ reducedMotion: 'reduce' });
      await page.route('**/__aria-test/**', (route) => {
        const name = new URL(route.request().url()).pathname.split('/').at(-1)!;
        if (name === 'index.html')
          return route.fulfill({
            contentType: 'text/html',
            headers: {
              'content-security-policy': [
                "default-src 'self'",
                "script-src 'self'",
                "style-src 'self' 'unsafe-inline'",
                "img-src 'self' data:",
                "font-src 'self' data:",
              ].join('; '),
            },
            body:
              '<!doctype html><html lang="en" data-theme="' +
              theme +
              '"><head>' +
              [...assets.keys()]
                .filter((key) => key.endsWith('.css'))
                .map((key) => '<link rel="stylesheet" href="' + key + '">')
                .join('') +
              '<style>body{margin:0;background:var(--nb-color-bg);color:var(--nb-color-text)}.scene{padding:24px;background:var(--nb-color-bg)}.fixture-native{color:var(--nb-color-text);background:var(--nb-color-surface);border:1px solid var(--nb-color-border);padding:24px;width:min(400px,80vw)}button,input{font:inherit}</style></head><body><div id="root"></div><script type="module" src="controls.js"></script></body></html>',
          });
        const asset = assets.get(name);
        return asset
          ? route.fulfill({ body: Buffer.from(asset.body), contentType: asset.contentType })
          : route.abort();
      });
      const pendingReads: import('@playwright/test').Route[] = [];
      await page.route('**/__aria-test/lock', (route) => {
        pendingReads.push(route);
      });
      await page.addInitScript(() =>
        document.addEventListener('securitypolicyviolation', (event) => {
          (window as unknown as { cspFailures: string[] }).cspFailures ??= [];
          (window as unknown as { cspFailures: string[] }).cspFailures.push(
            event.violatedDirective,
          );
        }),
      );
      await page.goto(FIXTURE_ORIGIN + '/__aria-test/index.html');
      const trigger = page.getByRole('button', { name: 'Edit service' });
      await trigger.click();
      const sheet = page.getByRole('dialog', { name: 'Service', exact: true });
      const draft = sheet.getByLabel('Draft');
      await draft.fill('saved draft');
      const background = page.getByRole('button', { name: 'Background action' });
      // Playwright's role locator does not account for native inert; inspect real browser semantics.
      expect(await background.evaluate((node) => Boolean(node.closest('[inert]')))).toBe(true);
      const accessibility = await page.context().newCDPSession(page);
      const tree = await accessibility.send('Accessibility.getFullAXTree');
      expect(
        tree.nodes.some((node) => !node.ignored && node.name?.value === 'Background action'),
      ).toBe(false);
      await accessibility.detach();
      const combo = sheet.getByRole('combobox', { name: 'Model' });
      await combo.fill('a');
      await combo.press('ArrowDown');
      await combo.press('Enter');
      await expect(combo).toHaveValue('alpha');
      const settings = sheet.getByRole('tab', { name: 'Settings' });
      const history = sheet.getByRole('tab', { name: 'History' });
      await settings.click();
      await page.keyboard.press('End');
      await expect(history).toBeFocused();
      await expect(sheet.getByRole('tabpanel', { name: 'History' })).toBeVisible();
      await page.keyboard.press('ArrowRight');
      await expect(settings).toBeFocused();
      await page.keyboard.press('Tab');
      await expect(sheet.getByRole('tabpanel', { name: 'Settings' })).toBeFocused();
      await page.keyboard.press('Shift+Tab');
      await expect(settings).toBeFocused();
      await page.keyboard.press('Home');
      await expect(settings).toBeFocused();

      // A background read locks the same visible controls and any already-open suggestion portal.
      await sheet.getByRole('button', { name: 'Load configuration' }).click();
      await combo.fill('a');
      await combo.press('ArrowDown');
      await expect(page.getByRole('listbox')).toBeVisible();
      await expect.poll(() => pendingReads.length).toBe(1);
      await pendingReads.shift()!.fulfill({ json: { locked: true } });
      await expect(combo).toBeDisabled();
      await expect(page.getByRole('listbox')).toHaveCount(0);
      const toggle = sheet.getByRole('switch', { name: 'Enabled' });
      await clickVisibleLabel(toggle);
      await expect(toggle).not.toBeChecked();
      await expect(combo).toHaveValue('a');
      await sheet.getByRole('button', { name: 'Unlock configuration' }).click();
      await sheet.getByRole('button', { name: 'Load configuration' }).click();
      const clear = sheet.getByRole('radio', { name: 'Clear', exact: true });
      await clear.locator('xpath=ancestor::label[1]').click();
      await expect(clear).toBeFocused();
      await expect.poll(() => pendingReads.length).toBe(1);
      await pendingReads.shift()!.fulfill({ json: { locked: true } });
      await expect(clear).toBeDisabled();
      await page.keyboard.press('ArrowLeft');
      await clickVisibleLabel(sheet.getByRole('radio', { name: 'Add', exact: true }));
      await expect(clear).toBeChecked();
      await sheet.getByRole('button', { name: 'Unlock configuration' }).click();
      await sheet.getByRole('button', { name: 'Open game dialog' }).click();
      const game = page.getByRole('dialog', { name: 'Game dialog', exact: true });
      const nativeElement = page.locator('dialog.fixture-native');
      await game.getByRole('button', { name: 'Game actions' }).click();
      const menu = page.getByRole('menu');
      expect(await menu.evaluate((node) => Boolean(node.closest('dialog:modal')))).toBe(true);
      await page.keyboard.press('End');
      await expect(page.getByRole('menuitem', { name: 'Other action' })).toBeFocused();
      await page.keyboard.press('Home');
      await page.keyboard.press('Enter');
      const confirm = page.getByRole('alertdialog', { name: 'Remove item?' });
      await expect(confirm.getByRole('button', { name: 'Cancel' })).toBeFocused();
      for (let i = 0; i < 4; i++) {
        await page.keyboard.press('Tab');
        expect(await confirm.evaluate((node) => node.contains(document.activeElement))).toBe(true);
      }

      if (process.env.NONBIRI_VISUAL_DIR) {
        mkdirSync(process.env.NONBIRI_VISUAL_DIR, { recursive: true });
        await page.screenshot({
          path: resolve(
            process.env.NONBIRI_VISUAL_DIR,
            'aria-nested-' + width + '-' + theme + '.png',
          ),
        });
      }
      await page.keyboard.press('Escape');
      await expect(confirm).toHaveCount(0);
      await expect(game).toBeVisible();
      await expect(nativeElement).toHaveJSProperty('open', true);
      await page.keyboard.press('Escape');
      await expect(game).toHaveCount(0);
      await expect(nativeElement).toHaveCount(0);
      await expect(sheet).toBeVisible();
      await sheet.getByRole('button', { name: 'Enter full screen' }).click();
      await expect.poll(() => page.evaluate(() => Boolean(document.fullscreenElement))).toBe(true);
      expect(await sheet.evaluate((node) => document.fullscreenElement?.contains(node))).toBe(true);
      await draft.fill('fullscreen draft');
      await sheet.getByRole('button', { name: 'Exit full screen' }).click();
      await expect.poll(() => page.evaluate(() => Boolean(document.fullscreenElement))).toBe(false);
      await expect(draft).toHaveValue('fullscreen draft');
      await page.keyboard.press('Escape');
      await expect(trigger).toBeFocused();
      await trigger.click();
      await expect(page.getByLabel('Draft')).toHaveValue('fullscreen draft');
      expect(violations).toEqual([]);
      expect(
        await page.evaluate(
          () => (window as unknown as { cspFailures?: string[] }).cspFailures ?? [],
        ),
      ).toEqual([]);
    });
  }
