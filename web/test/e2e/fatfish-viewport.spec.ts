import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { contentHash, seedCommitForVersion } from '../../src/shared/fatfish/engine/canonical';
import type { Level } from '../../src/shared/fatfish/engine/types';
import { expect, test } from './test';
import { ADMIN_ORIGIN } from './ports';
import { mockJson, mockPublicConfig, mockRoleSession } from './support';
import copy from '../../src/admin/i18n/en.json' with { type: 'json' };

const base = '/admin/api/limited-activities/fat-fish';
const text = copy.admin.fatfish.workspace;
const sizes = [
  { width: 1440, height: 900, name: '1440' },
  { width: 1366, height: 768, name: '1366' },
  { width: 1920, height: 1080, name: '1920' },
  { width: 1152, height: 720, name: '1440-125pct' },
  { width: 1093, height: 614, name: '1366-125pct' },
  { width: 390, height: 844, name: 'mobile' },
];

test('fatfish editor and playtest fit the whole field without internal scrolling', async ({
  page,
}) => {
  await page.addInitScript(() => localStorage.setItem('nb.lang', 'en'));
  await mockPublicConfig(page, 'admin');
  await mockRoleSession(page, 'admin', 'admin');
  const pageOf = (items: unknown[]) => ({ items, page: 1, page_size: 20, has_more: false });
  await mockJson(page, {
    origin: ADMIN_ORIGIN,
    method: 'GET',
    path: base + '/levels?page=1',
    body: pageOf([]),
  });
  await mockJson(page, {
    origin: ADMIN_ORIGIN,
    method: 'GET',
    path: base + '/playtests/current',
    body: null,
  });
  await mockJson(page, {
    origin: ADMIN_ORIGIN,
    method: 'GET',
    path: '/examples/fatfish/manifest.json',
    body: { format: 'nonbiri-fatfish-examples', version: 1, license: 'AGPL-3.0', examples: [] },
  });
  let draft: Level;
  let saved: Record<string, unknown>;
  let version: Record<string, unknown>;
  await page.route(ADMIN_ORIGIN + base + '/levels/validate', async (route) => {
    draft = route.request().postDataJSON().level;
    await route.fulfill({ json: { content_hash: contentHash(draft) } });
  });
  await page.route(ADMIN_ORIGIN + base + '/levels', async (route) => {
    saved = {
      ...route.request().postDataJSON(),
      id: 'ffl_layout',
      revision: '1',
      created_at: 1,
      updated_at: 1,
    };
    await route.fulfill({ json: saved });
  });
  await page.route(ADMIN_ORIGIN + base + '/levels/ffl_layout', (route) =>
    route.fulfill({ json: saved }),
  );
  await page.route(ADMIN_ORIGIN + base + '/levels/ffl_layout/versions', async (route) => {
    version = {
      id: 'ffv_layout',
      level_id: 'ffl_layout',
      version_number: '1',
      content_hash: contentHash(draft),
      created_at: 1,
    };
    await route.fulfill({ json: version });
  });
  await page.route(ADMIN_ORIGIN + base + '/levels/ffl_layout/versions?page=1', (route) =>
    route.fulfill({ json: pageOf([version]) }),
  );
  const seed = new Uint8Array(32);
  const prepared = () => ({
    id: 'ffc_layout',
    revision: '1',
    version_id: 'ffv_layout',
    state: 'prepared',
    content_hash: contentHash(draft),
    engine_version: draft.engine_version,
    scoring_version: 1,
    seed_commit: seedCommitForVersion(
      'ffc_layout',
      'ffv_layout',
      'ffv_layout',
      contentHash(draft),
      draft.engine_version,
      1,
      seed,
    ),
    prepared_at_ms: Date.now(),
    prepare_until_ms: Date.now() + 60000,
    start_at_ms: null,
    end_at_ms: null,
    submit_until_ms: null,
    server_now_ms: Date.now(),
    ticket_price: '0',
  });
  let active: Record<string, unknown> | null = null;
  await page.route(ADMIN_ORIGIN + base + '/playtests', (route) =>
    route.fulfill({ json: prepared() }),
  );
  await page.route(ADMIN_ORIGIN + base + '/playtests/ffc_layout/start', async (route) => {
    const now = Date.now();
    active = {
      ...prepared(),
      revision: '2',
      state: 'active',
      level: draft,
      seed: '00'.repeat(32),
      start_at_ms: now,
      end_at_ms: now + 300000,
      submit_until_ms: now + 330000,
      server_now_ms: now,
    };
    await route.fulfill({ json: active });
  });
  await page.route(ADMIN_ORIGIN + base + '/playtests/ffc_layout', (route) =>
    route.fulfill({ json: active ?? prepared() }),
  );
  await page.goto(ADMIN_ORIGIN + '/limited-activities/fat-fish');
  await expect(page.getByLabel(text.canvas_label)).toBeVisible();
  const directory = process.env.NONBIRI_VISUAL_DIR;
  if (directory) await mkdir(directory, { recursive: true });
  const measurements: unknown[] = [];
  const measure = async (kind: 'editor' | 'playtest') => {
    for (const size of sizes) {
      await page.setViewportSize(size);
      const viewport = page.locator(
        kind === 'editor'
          ? '.fatfish-editor-viewport'
          : '.fatfish-playtest .fatfish-player__viewport',
      );
      const board = viewport.locator(
        kind === 'editor' ? '.fatfish-level-stage' : '.fatfish-player__board',
      );
      const header = await page.locator('.nb-admin-header').boundingBox();
      const top = (header?.height ?? 0) + 16;
      await viewport.evaluate((node, offset) => {
        node.scrollIntoView({ block: 'start' });
        window.scrollBy(0, -offset);
      }, top);
      const bounds = await viewport.evaluate((node) => ({
        clientWidth: node.clientWidth,
        clientHeight: node.clientHeight,
        scrollWidth: node.scrollWidth,
        scrollHeight: node.scrollHeight,
      }));
      const rect = await board.boundingBox();
      if (!rect) throw new Error('The complete field is not rendered.');
      measurements.push({
        kind,
        ...size,
        ...bounds,
        boardWidth: rect.width,
        boardHeight: rect.height,
      });
      expect(bounds.scrollHeight, kind + ' vertical overflow at ' + size.name).toBeLessThanOrEqual(
        bounds.clientHeight + 1,
      );
      expect(bounds.scrollWidth).toBeLessThanOrEqual(bounds.clientWidth + 1);
      expect(rect.height).toBeLessThanOrEqual(size.height * 0.71);
      expect(rect.width / rect.height).toBeCloseTo(736 / 816, 2);
      expect(rect.y).toBeGreaterThanOrEqual((header?.height ?? 0) - 1);
      expect(rect.y + rect.height).toBeLessThanOrEqual(size.height + 1);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      if (directory)
        await page.screenshot({
          path: join(directory, `after-${kind}-${size.name}.png`),
          animations: 'disabled',
        });
      if (directory && [1440, 390].includes(size.width)) {
        await page.locator('.nb-account-trigger').click();
        await page.getByRole('combobox', { name: 'Theme', exact: true }).selectOption('dark');
        await page.keyboard.press('Escape');
        await page.screenshot({
          path: join(directory, `after-${kind}-${size.name}-dark.png`),
          animations: 'disabled',
        });
        await page.locator('.nb-account-trigger').click();
        await page.getByRole('combobox', { name: 'Theme', exact: true }).selectOption('light');
        await page.keyboard.press('Escape');
      }
    }
  };
  await measure('editor');
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.getByRole('button', { name: text.zoom_in, exact: true }).click();
  const editorViewport = page.locator('.fatfish-editor-viewport');
  expect(await editorViewport.evaluate((node) => node.scrollHeight > node.clientHeight)).toBe(true);
  await page.getByRole('button', { name: text.fit_screen, exact: true }).click();
  expect(await editorViewport.evaluate((node) => node.scrollHeight <= node.clientHeight + 1)).toBe(
    true,
  );
  await page.getByLabel(text.title, { exact: true }).fill('Viewport level');
  await page.getByLabel(text.duration_seconds, { exact: true }).fill('300');
  await page.getByRole('button', { name: text.playtest_this_level, exact: true }).click();
  await page.getByRole('button', { name: text.start_playtest, exact: true }).click();
  await expect(page.locator('.fatfish-playtest .fatfish-player__board')).toBeVisible();
  await measure('playtest');
  const player = page.locator('.fatfish-playtest .fatfish-player');
  await player.getByRole('button', { name: 'Enlarge field', exact: true }).click();
  expect(
    await player
      .locator('.fatfish-player__viewport')
      .evaluate((node) => node.scrollHeight > node.clientHeight),
  ).toBe(true);
  await player.getByRole('button', { name: 'Fit screen', exact: true }).click();
  expect(
    await player
      .locator('.fatfish-player__viewport')
      .evaluate((node) => node.scrollHeight <= node.clientHeight + 1),
  ).toBe(true);
  if (directory)
    await writeFile(
      join(directory, 'after-bounds.json'),
      JSON.stringify(measurements, null, 2) + '\n',
    );
});
