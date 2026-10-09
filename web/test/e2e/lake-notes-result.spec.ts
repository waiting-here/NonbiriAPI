import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import { createServer } from 'vite';
import { expect, test } from './test';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { USER_ORIGIN } from './ports';
import type { CastResult, ProfileView } from '../../src/user/activities/lake-notes/api';

test.use({ locale: 'zh-CN' });

let rules: typeof import('../../src/user/activities/lake-notes/rules');
test.beforeAll(async () => {
  // Load the JSON catalog with the same transforms as the application.
  const server = await createServer({
    configFile: false,
    optimizeDeps: { noDiscovery: true, include: [] },
    server: { middlewareMode: true, watch: null },
  });
  try {
    rules = (await server.ssrLoadModule(
      '/src/user/activities/lake-notes/rules/index.ts',
    )) as typeof rules;
  } finally {
    await server.close();
  }
});

for (const width of [1440, 390]) {
  for (const theme of ['light', 'dark']) {
    test('debris confirmation stays live at ' + width + 'px in ' + theme, async ({ page }) => {
      const { initialProfile, RULES_ID, start, step } = rules;
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
      await page.addInitScript((value) => localStorage.setItem('nb.theme', value), theme);
      const consoleErrors = collectConsoleViolations(page);
      await mockRoleSession(page, 'user', 'user');
      await mockPublicConfig(page, 'user');
      const profile = initialProfile();
      profile.records.lake_carp = {
        caught: '1',
        maxLength: 30,
        bestQuality: 0,
        perfectCount: '0',
      };
      let view: ProfileView = {
        readonly: false,
        revision: '1',
        rules_id: RULES_ID,
        profile,
        cast: null,
        wallet: { general_milli: '0', game_milli: '0' },
        settings: {
          revision: '1',
          enabled: true,
          exchanges: {
            coins_to_general: { enabled: false, source_amount: '', target_amount: '' },
            general_to_coins: { enabled: false, source_amount: '', target_amount: '' },
            coins_to_game: { enabled: false, source_amount: '', target_amount: '' },
            game_to_coins: { enabled: false, source_amount: '', target_amount: '' },
          },
        },
      };
      let starts = 0;
      let checkpoints = 0;
      let confirm!: () => void;
      const confirmation = new Promise<void>((resolve) => {
        confirm = resolve;
      });
      await page.route(USER_ORIGIN + '/api/games/lake-notes/**', async (route) => {
        const request = route.request();
        const path = new URL(request.url()).pathname;
        if (request.method() === 'GET' && path.endsWith('/profile')) {
          await route.fulfill({ json: view });
        } else if (request.method() === 'POST' && path.endsWith('/casts')) {
          starts++;
          const prediction = start(view.profile, () => 0, 1);
          expect(prediction.cast.plan.debris).toBeTruthy();
          const cast: CastResult['cast'] = {
            id: 'lnc_' + (starts === 1 ? 'A' : 'B').repeat(22),
            rules_id: RULES_ID,
            generation: '1',
            revision: '1',
            ack_tick: 0,
            phase: 'waiting',
            paused: false,
            readonly: false,
            state: prediction.cast,
            profile_revision: view.revision,
          };
          view = { ...view, profile: prediction.profile, cast };
          await route.fulfill({ json: { cast, profile: view } });
        } else if (request.method() === 'POST' && path.endsWith('/checkpoint')) {
          checkpoints++;
          const cast = structuredClone(view.cast!);
          const savedProfile = structuredClone(view.profile);
          const input = request.postDataJSON();
          for (let tick = cast.state.tick; tick < input.to_tick; tick++)
            step(savedProfile, cast.state, false);
          expect(cast.state.result?.debris).toBeTruthy();
          expect(cast.state.fish).toBeUndefined();
          cast.phase = 'success';
          cast.ack_tick = cast.state.tick;
          cast.revision = '2';
          cast.profile_revision = String(BigInt(view.revision) + 1n);
          await confirmation;
          view = { ...view, revision: cast.profile_revision, profile: savedProfile, cast };
          await route.fulfill({ json: { cast, profile: view } });
        } else await route.fallback();
      });
      await page.goto(USER_ORIGIN + '/games/lake-notes');
      await page.locator('.lake-original #startButton').click();
      const title = page.locator('.lake-original #overlayTitle');
      const button = page.locator('.lake-original #overlayButton');
      await expect(title).toHaveText('正在确认结果');
      await expect(title).toBeVisible();
      await expect(button).toBeDisabled();
      await expect(page.locator('.lake-original #fish')).toHaveCSS('opacity', '0');
      await expect(page.locator('.lake-original #catchBar')).not.toHaveClass(/\bhit\b/);
      consoleErrors.assertNone();
      const folder = 'test-results/lake-trash-result';
      await mkdir(folder, { recursive: true });
      await page.screenshot({
        path: join(folder, 'confirming-' + theme + '-' + width + '.png'),
        animations: 'disabled',
      });
      confirm();
      await expect(title).toHaveText('捞起一份杂物');
      await expect(title).toBeVisible();
      await expect(button).toBeEnabled();
      await expect
        .poll(() =>
          page
            .locator('.lake-original #catchArt')
            .evaluate((image) => (image as HTMLImageElement).naturalWidth),
        )
        .toBeGreaterThan(0);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      await page.screenshot({
        path: join(folder, 'confirmed-' + theme + '-' + width + '.png'),
        animations: 'disabled',
      });
      await button.click();
      await expect.poll(() => starts).toBe(2);
      await expect.poll(() => checkpoints).toBe(2);
      await expect(title).toHaveText('捞起一份杂物');
      consoleErrors.assertNone();
    });
  }
}
