import { readFileSync, mkdirSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { expect, test } from './test';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { USER_ORIGIN } from './ports';
import { gamesSnapshotWire } from '../../src/user/games/common/testFixtures';
import type { Card, CardDefinition, View } from '../../src/user/games/gwent/types';

const cards = JSON.parse(
  readFileSync('../internal/game/gwent/engine/cards.json', 'utf8'),
) as CardDefinition[];
const card = (id: string, instance_id: number): Card => {
  const definition = cards.find((item) => item.id === id)!;
  return { ...definition, instance_id, base_power: definition.power };
};
const contentHash = createHash('sha256')
  .update(JSON.stringify({ rules_version: 1, cards }))
  .digest('hex');
function home() {
  const player = {
    faction: 'openai' as const,
    lives: 2,
    passed: false,
    hand_count: 10,
    deck_count: 20,
    grave: [],
    leader: card('openai_leader', 1),
    leader_available: true,
    boost: 0,
    shield: false,
  };
  const view: View = {
    version: 1,
    round: 1,
    phase: 'turn',
    turn: 0,
    self: player,
    enemy: { ...player, faction: 'claude', leader: card('claude_leader', 2) },
    hand: Array.from({ length: 10 }, (_, i) =>
      card(i % 2 ? 'openai_reasoner' : 'openai_agent', 10 + i),
    ),
    board: (['enemy', 'self'] as const).flatMap((side, i) =>
      ['siege', 'ranged', 'close'].map((row, j) => ({
        side,
        row,
        total: 18,
        weather: false,
        cards: Array.from({ length: 6 }, (_, k) =>
          card('openai_agent', 100 + i * 100 + j * 10 + k),
        ),
      })),
    ),
    weather: [],
    rounds: [],
    legal_actions: [{ kind: 'play', card: 10, row: 'close' }, { kind: 'pass' }],
  };
  return {
    server_now: 1800000000,
    queue: null,
    latest_result: null,
    current: {
      id: `gwt_${'A'.repeat(22)}`,
      game: 'gwent',
      mode: 'standard',
      rules_version: 1,
      content_hash: contentHash,
      revision: '3',
      phase_seq: '2',
      decision_id: '2',
      phase: 'turn',
      round: 1,
      deadline: 1800000030,
      server_now: 1800000000,
      you: 0,
      locked: [false, true],
      ticket: '1',
      rake_bp: { platform: 0, welfare: 0, thursday: 0 },
      own_payment: { general: '1', game: '0' },
      profiles: [
        { kind: 'anonymous' },
        { kind: 'public', display_name: 'Card player', avatar_url: null },
      ],
      resolution: null,
      round_start: null,
      view,
    },
  };
}

for (const theme of ['light', 'dark']) {
  test(`card battle and choices remain usable across viewports in ${theme}`, async ({ page }) => {
    const errors = collectConsoleViolations(
      page,
      'An iframe which has both allow-scripts and allow-same-origin for its sandbox attribute can escape its sandboxing.',
    );
    await page.addInitScript((theme) => {
      localStorage.setItem('nb.lang', 'en');
      localStorage.setItem('nb.theme', theme);
    }, theme);
    await mockRoleSession(page, 'user', 'user');
    await mockPublicConfig(page, 'user');
    const snapshot = gamesSnapshotWire(),
      state = home(),
      writes: unknown[] = [];
    snapshot.gwent.enabled = true;
    snapshot.gwent.modes.standard.enabled = true;
    await page.route('**/api/games**', async (route) => {
      const request = route.request(),
        url = new URL(request.url()),
        path = url.pathname;
      if (path === '/api/games') return route.fulfill({ json: snapshot });
      if (path === '/api/games/gwent/state') return route.fulfill({ json: state });
      if (path === '/api/games/gwent/ai')
        return route.fulfill({ json: { enabled: false, bots: [] } });
      if (path.includes('/randomness/')) return route.fulfill({ json: { proof: null } });
      if (path.endsWith('/catalog'))
        return route.fulfill({
          json: {
            content_hash: contentHash,
            modes: { standard: { rules_version: 1, cards }, ai: { rules_version: 1, cards } },
          },
        });
      if (path.endsWith('/actions')) {
        writes.push(request.postDataJSON());
        state.current.locked = [true, false];
        state.current.phase_seq = '3';
        state.current.decision_id = '3';
        state.current.view.legal_actions = [];
        return route.fulfill({
          json: { session_id: state.current.id, revision: '4', phase_seq: '3', locked: true },
        });
      }
      if (path.endsWith('/leaderboard'))
        return route.fulfill({
          json: { window: url.searchParams.get('window'), rows: [], me: null },
        });
      return route.fallback();
    });
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto(`${USER_ORIGIN}/games/gwent`);
    const arena = page.frameLocator('iframe.gwent-original-frame');
    await expect(arena.locator('#arena-stats-op')).toContainText('Card player');
    await expect(arena.locator('.arena-row')).toHaveCount(6);
    for (const size of [
      { width: 1440, height: 900 },
      { width: 1280, height: 720 },
      { width: 390, height: 844 },
      { width: 320, height: 740 },
      { width: 720, height: 420 },
    ]) {
      await page.setViewportSize(size);
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1),
        JSON.stringify(size),
      ).toBe(true);
      await expect
        .poll(
          () =>
            arena
              .locator('body')
              .evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1),
          { message: JSON.stringify(size) },
        )
        .toBe(true);
      const hand = await arena.locator('#arena-hand').boundingBox();
      expect(hand!.height).toBeGreaterThan(0);
      const rows = await arena.locator('.arena-row').all();
      for (const row of rows) {
        const bounds = await row.boundingBox();
        expect(bounds!.width).toBeGreaterThan(0);
        expect(bounds!.height).toBeGreaterThan(0);
      }
      await arena.locator('#arena-hand button').first().click();
      await expect(arena.locator('#selection-name')).toContainText(
        cards.find((c) => c.id === 'openai_agent')!.name,
      );
      await expect(arena.locator('#arena-me-close')).toHaveClass(/target-row/);
      if (process.env.NONBIRI_GAME_PRESENTATION_EVIDENCE && [1280, 390].includes(size.width)) {
        mkdirSync(process.env.NONBIRI_GAME_PRESENTATION_EVIDENCE, { recursive: true });
        await page.screenshot({
          path: `${process.env.NONBIRI_GAME_PRESENTATION_EVIDENCE}/gwent-${theme}-${size.width}.png`,
          fullPage: true,
        });
      }
      // Toggle the same selection off so the next viewport begins in the same state.
      await arena.locator('#arena-hand button').first().click();
    }
    expect(writes).toEqual([]);
    await arena.locator('#arena-hand button').first().click();
    const submitted = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname.endsWith('/actions') &&
        response.request().method() === 'POST',
    );
    await arena.locator('#arena-me-close .row-info').click();
    expect((await submitted).status()).toBe(200);
    expect(writes).toEqual([
      { phase_seq: '2', decision_id: '2', action: { kind: 'play', card: 10, row: 'close' } },
    ]);
    await expect(arena.locator('#battle')).not.toHaveClass(/my-turn/);
    errors.assertNone();
  });
}
