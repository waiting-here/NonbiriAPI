import { contentHash, seedCommitForVersion } from '../../src/shared/fatfish/engine/canonical';
import type { Level } from '../../src/shared/fatfish/engine/types';
import { expect, test } from './test';
import { ADMIN_ORIGIN } from './ports';
import { collectConsoleViolations, mockJson, mockPublicConfig, mockRoleSession } from './support';
import en from '../../src/admin/i18n/en.json' with { type: 'json' };
import zh from '../../src/admin/i18n/zh.json' with { type: 'json' };

const base = '/admin/api/limited-activities/fat-fish';
for (const language of ['en', 'zh'] as const) {
  test('polygon gestures preserve exact coordinates and valid snapshots in ' + language, async ({ page }) => {
    const text = (language === 'en' ? en : zh).admin.fatfish.workspace;
    const violations = collectConsoleViolations(page);
    await page.addInitScript((lang) => localStorage.setItem('nb.lang', lang), language);
    await mockPublicConfig(page, 'admin'); await mockRoleSession(page, 'admin', 'admin');
    await mockJson(page, { origin: ADMIN_ORIGIN, method: 'GET', path: base + '/levels?page=1', body: { items: [], page: 1, page_size: 20, has_more: false } });
    await mockJson(page, { origin: ADMIN_ORIGIN, method: 'GET', path: base + '/playtests/current', body: null });
    await mockJson(page, { origin: ADMIN_ORIGIN, method: 'GET', path: '/examples/fatfish/manifest.json', body: { format: 'nonbiri-fatfish-examples', version: 1, license: 'AGPL-3.0', examples: [] } });
    let draft: Record<string, unknown> | null = null;
    await page.route(ADMIN_ORIGIN + base + '/levels', async (route) => {
      draft = route.request().postDataJSON();
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ...draft, id: 'ffl_fixture', revision: '1', created_at: 1, updated_at: 1 }) });
    });
    await page.route(ADMIN_ORIGIN + base + '/levels/ffl_fixture', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ ...draft, id: 'ffl_fixture', revision: '1', created_at: 1, updated_at: 1 }) }));
    await mockJson(page, { origin: ADMIN_ORIGIN, method: 'GET', path: base + '/levels/ffl_fixture/versions?page=1', body: { items: [], page: 1, page_size: 20, has_more: false } });
    await page.goto(ADMIN_ORIGIN + '/limited-activities/fat-fish');
    await page.getByRole('button', { name: text.add + ' ' + text.fixed_obstacle, exact: true }).click();
    await page.getByText(text.advanced_contours_and_holes, { exact: true }).click();
    const contour = page.getByRole('group', { name: text.outer_contour, exact: true });
    await contour.getByLabel('X', { exact: true }).first().fill('216.015625');
    await expect(contour.getByLabel('X', { exact: true }).first()).toHaveValue('216.015625');
    await page.getByRole('button', { name: text.edit_vertices, exact: true }).click();
    const handle = page.locator('.fatfish-level-map circle[aria-label]').first();
    await handle.scrollIntoViewIfNeeded();
    const rect = await handle.boundingBox(); if (!rect) throw new Error('Vertex is not visible');
    await page.mouse.move(rect.x + rect.width / 2, rect.y + rect.height / 2);
    await page.mouse.down(); await page.mouse.move(rect.x + rect.width / 2 + 16, rect.y + rect.height / 2 + 8); await page.mouse.up();
    await expect(contour.getByLabel('X', { exact: true }).first()).not.toHaveValue('216.015625');
    await page.getByRole('button', { name: text.undo, exact: true }).click();
    await page.getByText(text.advanced_contours_and_holes, { exact: true }).click();
    await expect(contour.getByLabel('X', { exact: true }).first()).toHaveValue('216.015625');
    await page.getByRole('button', { name: text.draw_hole, exact: true }).click();
    const stage = page.locator('.fatfish-level-map');
    await stage.scrollIntoViewIfNeeded();
    const box = await stage.boundingBox(); if (!box) throw new Error('Stage is not visible');
    const click = async (x: number, y: number) => page.mouse.click(box.x + (x + 128) * box.width / 736, box.y + (y + 128) * box.height / 816);
    await click(228, 272); await click(252, 272); await click(252, 288); await click(228, 288);
    await page.getByRole('button', { name: text.finish_contour, exact: true }).click();
    await expect(page.getByRole('group', { name: text.hole + ' 1', exact: true })).toBeVisible();
    const hole = page.getByRole('group', { name: text.hole + ' 1', exact: true });
    await stage.scrollIntoViewIfNeeded();
    const holeBox = await stage.boundingBox(); if (!holeBox) throw new Error('Stage is not visible');
    const holeEdge = page.locator('.fatfish-level-map line[stroke="transparent"]').nth(4);
    await holeEdge.scrollIntoViewIfNeeded();
    const edgeBox = await holeEdge.boundingBox(); if (!edgeBox) throw new Error('Hole edge is not visible');
    await page.mouse.click(edgeBox.x + edgeBox.width / 2, edgeBox.y + edgeBox.height / 2);
    await expect(page.locator('.fatfish-level-map circle[aria-label]')).toHaveCount(9);
    await expect(page.getByRole('button', { name: text.save_draft, exact: true })).toBeDisabled();
    const inserted = page.locator('.fatfish-level-map circle[aria-label]').nth(5);
    await inserted.scrollIntoViewIfNeeded();
    const insertedBox = await inserted.boundingBox(); if (!insertedBox) throw new Error('Inserted vertex is not visible');
    await page.mouse.move(insertedBox.x + insertedBox.width / 2, insertedBox.y + insertedBox.height / 2);
    await page.mouse.down();
    await page.mouse.move(insertedBox.x + insertedBox.width / 2, insertedBox.y + insertedBox.height / 2 + 8 * holeBox.height / 816);
    await page.mouse.up();
    await expect(hole.getByLabel('X', { exact: true })).toHaveCount(5);
    await stage.press('Delete');
    await expect(hole.getByLabel('X', { exact: true })).toHaveCount(4);
    await page.getByLabel(text.move_selected_hole, { exact: true }).check();
    const holeHandle = page.locator('.fatfish-level-map circle[aria-label]').nth(4);
    await holeHandle.scrollIntoViewIfNeeded();
    const holeVertex = await holeHandle.boundingBox(); if (!holeVertex) throw new Error('Hole vertex is not visible');
    await page.mouse.move(holeVertex.x + holeVertex.width / 2, holeVertex.y + holeVertex.height / 2);
    await page.mouse.down();
    await page.mouse.move(holeVertex.x + holeVertex.width / 2 - 8 * holeBox.width / 736, holeVertex.y + holeVertex.height / 2);
    await page.mouse.up();
    await expect(hole.getByLabel('X', { exact: true }).first()).toHaveValue('224');
    await expect(hole.getByLabel('X', { exact: true }).nth(1)).toHaveValue('248');
    await page.getByRole('button', { name: text.undo, exact: true }).click();
    await page.getByText(text.advanced_contours_and_holes, { exact: true }).click();
    await expect(hole.getByLabel('X', { exact: true }).first()).toHaveValue('232');

    await page.getByRole('button', { name: text.draw_outer_contour, exact: true }).click();
    await click(10, 10); await click(20, 20);
    await expect(page.getByRole('button', { name: text.save_draft, exact: true })).toBeDisabled();
    await stage.press('Escape');
    await page.getByLabel(text.title, { exact: true }).fill('Gesture level');
    await page.getByRole('button', { name: text.save_draft, exact: true }).click();
    await expect.poll(() => draft).not.toBeNull();
    const saved = draft as unknown as { draft: { solids: Array<{ polygon: { outer: Array<{ x: number }>; holes: unknown[] } }> } };
    expect(saved.draft.solids[0].polygon.outer[0].x).toBe(216 * 64 + 1);
    expect(saved.draft.solids[0].polygon.holes).toHaveLength(1);
    await page.setViewportSize({ width: language === 'zh' ? 390 : 1280, height: 900 });
    await expect(page.getByRole('button', { name: text.playtest_this_level, exact: true })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.screenshot({ path: '../.pi/fatfish-workspace/workspace-' + language + '.png', fullPage: true, animations: 'disabled' });
    violations.assertNone();
  });
}

test('graph gestures save independent layout and preserve the target condition and dirty fields', async ({ page }) => {
  const text = en.admin.fatfish.workspace, violations = collectConsoleViolations(page);
  await page.addInitScript(() => localStorage.setItem('nb.lang', 'en'));
  await mockPublicConfig(page, 'admin'); await mockRoleSession(page, 'admin', 'admin');
  const amounts = { unlock_cost: '0', ticket_price: '0', first_clear_reward: '0', star_rewards: ['0', '0', '0'] };
  const node = (id: string, title: string, x: number, order: number, condition: unknown) => ({
    id, period_id: 'ffp_graph', title, description: '', map_x: x, map_y: 200, order,
    revision: '1', version_id: 'ffv_graph', condition, hidden: false, amounts,
  });
  const a = node('ffn_a', 'Source', 160, 0, {});
  const b = node('ffn_b', 'Target', 400, 1, { all: [{ total_stars: 2 }, { any: [{ stars: { node: 'ffn_a', min: 2 } }, { passed_count: 1 }] }] });
  const period = { id: 'ffp_graph', title: 'Graph period', description: '', state: 'draft', visible: false, paused: false, past_public: false,
    starts_at: 1917000000, ends_at: 1919000000, revision: '7', leaderboard_final: false, nodes: [a, b] };
  const level = { id: 'ffl_graph', title: 'Graph level', description: '', revision: '1', created_at: 1, updated_at: 1 };
  const version = { id: 'ffv_graph', level_id: level.id, version_number: '12', content_hash: 'a'.repeat(64), playtest_summary: { count: 1, best_stars: 2 }, created_at: 1 };
  const pageOf = (items: unknown[]) => ({ items, page: 1, page_size: 20, has_more: false });
  await mockJson(page, { origin: ADMIN_ORIGIN, method: 'GET', path: base + '/periods?page=1', body: pageOf([period]) });
  await mockJson(page, { origin: ADMIN_ORIGIN, method: 'GET', path: base + '/periods/ffp_graph', body: period });
  await mockJson(page, { origin: ADMIN_ORIGIN, method: 'GET', path: base + '/levels?page=1', body: pageOf([level]) });
  await mockJson(page, { origin: ADMIN_ORIGIN, method: 'GET', path: base + '/versions/ffv_graph', body: version });
  await mockJson(page, { origin: ADMIN_ORIGIN, method: 'GET', path: base + '/levels/ffl_graph/versions?page=1', body: pageOf([version]) });
  for (const entry of [a, b]) await mockJson(page, { origin: ADMIN_ORIGIN, method: 'GET', path: base + '/periods/ffp_graph/nodes/' + entry.id, body: entry });
  let layoutBody: { expected_revision: string; nodes: Array<{ node_id: string; map_x: number; map_y: number }> } | null = null;
  let nodeBody: Record<string, unknown> | null = null;
  await page.route(ADMIN_ORIGIN + base + '/periods/ffp_graph/layout', async (route) => {
    if (route.request().method() === 'PUT') {
      layoutBody = route.request().postDataJSON();
      await route.fulfill({ status: 200, json: { revision: '1', nodes: layoutBody!.nodes } });
    } else await route.fulfill({ status: 200, json: { revision: '0', nodes: [a, b].map((entry) => ({ node_id: entry.id, map_x: entry.map_x, map_y: entry.map_y })) } });
  });
  await page.route(ADMIN_ORIGIN + base + '/periods/ffp_graph/nodes/ffn_b', async (route) => {
    if (route.request().method() === 'PUT') {
      nodeBody = route.request().postDataJSON();
      await route.fulfill({ status: 200, json: { ...b, ...nodeBody, revision: '2' } });
    } else await route.fulfill({ status: 200, json: b });
  });
  await page.goto(ADMIN_ORIGIN + '/limited-activities/fat-fish?tab=periods');
  await page.getByRole('button', { name: 'Graph period · draft', exact: true }).click();
  await page.getByRole('button', { name: /^2\. Target/ }).click();
  const editor = page.locator('.fatfish-node-editor');
  await editor.getByLabel(text.title, { exact: true }).fill('Target unsaved');
  const graph = page.getByRole('img', { name: text.draggable_node_layout, exact: true });
  await graph.scrollIntoViewIfNeeded();
  let box = await graph.boundingBox(); if (!box) throw new Error('Graph not visible');
  const at = (x: number, y: number) => ({ x: box!.x + x * box!.width / 800, y: box!.y + y * box!.height / 500 });
  let point = at(400, 200);
  await page.mouse.move(point.x, point.y); await page.mouse.down();
  point = at(432, 224); await page.mouse.move(point.x, point.y); await page.mouse.up();
  await expect.poll(() => layoutBody).not.toBeNull();
  expect(layoutBody!.expected_revision).toBe('0');
  expect(layoutBody!.nodes.find((entry) => entry.node_id === 'ffn_b')).toEqual({ node_id: 'ffn_b', map_x: 432, map_y: 224 });
  expect(nodeBody).toBeNull();
  await expect(editor.getByLabel(text.title, { exact: true })).toHaveValue('Target unsaved');
  await graph.scrollIntoViewIfNeeded(); box = await graph.boundingBox();
  const source = at(192, 200), target = at(432, 224);
  await page.mouse.move(source.x, source.y); await page.mouse.down(); await page.mouse.move(target.x, target.y); await page.mouse.up();
  await expect(editor.locator('.fatfish-condition select').first()).toHaveValue('all');
  await editor.getByRole('button', { name: text.save_node, exact: true }).click();
  await expect.poll(() => nodeBody).not.toBeNull();
  expect(nodeBody!.title).toBe('Target unsaved');
  expect(nodeBody!.condition).toEqual({ all: [{ total_stars: 2 }, { any: [{ stars: { node: 'ffn_a', min: 2 } }, { passed_count: 1 }] }, { passed: 'ffn_a' }] });
  expect(nodeBody!.expected_period_revision).toBe('7');
  await page.setViewportSize({ width: 390, height: 900 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: '../.pi/fatfish-workspace/graph-narrow.png', fullPage: true, animations: 'disabled' });
  violations.assertNone();
});

test('draft playtest prepares one version, starts manually, and automatically persists terminal submission', async ({ page }) => {
  const text = en.admin.fatfish.workspace, violations = collectConsoleViolations(page);
  await page.addInitScript(() => localStorage.setItem('nb.lang', 'en'));
  await mockPublicConfig(page, 'admin'); await mockRoleSession(page, 'admin', 'admin');
  const pageOf = (items: unknown[]) => ({ items, page: 1, page_size: 20, has_more: false });
  await mockJson(page, { origin: ADMIN_ORIGIN, method: 'GET', path: base + '/levels?page=1', body: pageOf([]) });
  await mockJson(page, { origin: ADMIN_ORIGIN, method: 'GET', path: base + '/playtests/current', body: null });
  await mockJson(page, { origin: ADMIN_ORIGIN, method: 'GET', path: '/examples/fatfish/manifest.json', body: { format: 'nonbiri-fatfish-examples', version: 1, license: 'AGPL-3.0', examples: [] } });
  let draft: Level;
  let saved: Record<string, unknown>;
  let version: Record<string, unknown>;
  const stages: string[] = [];
  await page.route(ADMIN_ORIGIN + base + '/levels/validate', async (route) => {
    stages.push('validate'); draft = route.request().postDataJSON().level;
    await route.fulfill({ status: 200, json: { content_hash: contentHash(draft) } });
  });
  await page.route(ADMIN_ORIGIN + base + '/levels', async (route) => {
    stages.push('save'); saved = { ...route.request().postDataJSON(), id: 'ffl_play', revision: '1', created_at: 1, updated_at: 1 };
    await route.fulfill({ status: 200, json: saved });
  });
  await page.route(ADMIN_ORIGIN + base + '/levels/ffl_play', (route) => route.fulfill({ status: 200, json: saved }));
  await page.route(ADMIN_ORIGIN + base + '/levels/ffl_play/versions', async (route) => {
    stages.push('version'); version = { id: 'ffv_play', level_id: 'ffl_play', version_number: '1', content_hash: contentHash(draft), created_at: 1 };
    await route.fulfill({ status: 200, json: version });
  });
  await page.route(ADMIN_ORIGIN + base + '/levels/ffl_play/versions?page=1', (route) => route.fulfill({ status: 200, json: pageOf([version]) }));
  const seed = new Uint8Array(32);
  const prepared = () => ({
    id: 'ffc_play', revision: '1', version_id: 'ffv_play', state: 'prepared', content_hash: contentHash(draft),
    engine_version: draft.engine_version, scoring_version: 1,
    seed_commit: seedCommitForVersion('ffc_play', 'ffv_play', 'ffv_play', contentHash(draft), draft.engine_version, 1, seed),
    prepared_at_ms: Date.now(), prepare_until_ms: Date.now() + 60000, start_at_ms: null, end_at_ms: null,
    submit_until_ms: null, server_now_ms: Date.now(), ticket_price: '0',
  });
  let starts = 0, submits = 0, reads = 0;
  let submitted: { tab_capability: string; terminal_tick: number; inputs: unknown[] } | null = null;
  let active: Record<string, unknown>;
  await page.route(ADMIN_ORIGIN + base + '/playtests', async (route) => {
    stages.push('prepare');
    expect(route.request().postDataJSON().version_id).toBe('ffv_play');
    await route.fulfill({ status: 200, json: prepared() });
  });
  await page.route(ADMIN_ORIGIN + base + '/playtests/ffc_play/start', async (route) => {
    starts++;
    const now = Date.now();
    active = { ...prepared(), revision: '2', state: 'active', level: draft, seed: '00'.repeat(32),
      start_at_ms: now - 11000, end_at_ms: now - 1000, submit_until_ms: now + 30000, server_now_ms: now };
    await route.fulfill({ status: 200, json: active });
  });
  await page.route(ADMIN_ORIGIN + base + '/playtests/ffc_play/submit', async (route) => {
    submits++; submitted = route.request().postDataJSON();
    await route.fulfill({ status: 200, json: { ...active, state: 'verifying', revision: '3' } });
  });
  await page.route(ADMIN_ORIGIN + base + '/playtests/ffc_play', async (route) => {
    reads++;
    await route.fulfill({ status: 200, json: active ? { ...active, state: submits ? 'settled_fail' : 'active', ...(submits ? { result: {
      state: 'settled_fail', reason: 'time_up', terminal_tick: 600, fed: 0, total: 1, bowl_counts: [],
      passed: false, stars: 0, score_units: '0', engine_version: draft.engine_version, scoring_version: 1,
      content_hash: contentHash(draft), final_state_hash: 'a'.repeat(64), seed_commit: active.seed_commit,
      commitment_verified: true, ticket_charge: '0', ticket_refund: '0', rewards: '0',
    } } : {}) } : prepared() });
  });
  await page.goto(ADMIN_ORIGIN + '/limited-activities/fat-fish');
  await page.getByLabel(text.title, { exact: true }).fill('Playtest level');
  await page.getByText(text.duration_speed_and_star_thresholds, { exact: true }).click();
  await page.getByLabel(text.duration_seconds, { exact: true }).fill('10');
  await page.getByRole('button', { name: text.playtest_this_level, exact: true }).click();
  await expect(page.getByRole('button', { name: text.start_playtest, exact: true })).toBeVisible();
  expect(stages).toEqual(['validate', 'save', 'version', 'prepare']);
  expect(starts).toBe(0); expect(submits).toBe(0);
  await page.getByRole('button', { name: text.start_playtest, exact: true }).click();
  await expect.poll(() => submits).toBe(1);
  expect(submitted!.terminal_tick).toBe(600);
  expect(submitted!.inputs).toEqual([]);
  expect(submitted!.tab_capability).toMatch(/^[0-9a-f]{64}$/);
  await expect(page.getByText(text.verification_is_in_progress_wait_for_this_result_before_starting_anoth, { exact: true })).toBeVisible();
  await expect(page.getByText('This challenge has ended.', { exact: true })).toBeVisible();
  expect(reads).toBeGreaterThan(0);
  expect(submits).toBe(1); expect(starts).toBe(1);
  expect(await page.locator('body').innerText()).not.toContain(submitted!.tab_capability);
  violations.assertNone();
});
