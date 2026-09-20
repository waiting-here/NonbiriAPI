import { createHash } from 'node:crypto';
import { mkdir, readFile } from 'node:fs/promises';
import { expect, test } from './test';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';
import { USER_ORIGIN } from './ports';
import { blackjackWire, tableID } from '../../src/user/games/blackjack/testFixtures';
import { biddingHomeWire, biddingID } from '../../src/user/games/bidding/testFixtures';
import { rpsStateWire, rpsTestSessionID } from '../../src/user/games/rps/testFixtures';
import { gamesSnapshotWire } from '../../src/user/games/common/testFixtures';
import rawCatalog from '../../../internal/game/likes/catalog/quick.json' with { type: 'json' };
import likes from '../../src/user/games/likes/testdata/authority.json' with { type: 'json' };

const seed = '2a'.repeat(32),
  algorithm = 'hmac-sha256-reject64-v1';
const fixed = {
  algorithm,
  game: 'blackjack',
  resource_id: tableID,
  rules: 'six-decks-s17-v1',
  commitment: '5b14b84868c9d5d5c346ad5ee4bea248526db7e6fa1ac8916a40c6c3f28f4260',
  seed,
  streams: [{ label: 'shoe', samples: 'AAAAAAAAATgAAAAAAAABBg==' }],
};
function opening(game: string, id: string) {
  const rules = 'fixture/v1';
  const domain = `nonbiri/game-random/${algorithm}\0commit\0${game}\0${id}\0${rules}\0`;
  return {
    algorithm,
    game,
    resource_id: id,
    rules,
    commitment: createHash('sha256').update(domain).update(Buffer.from(seed, 'hex')).digest('hex'),
  };
}
function catalog() {
  const config = structuredClone(rawCatalog) as unknown as Record<string, unknown>;
  delete (config.parameters as Record<string, number>).POINT_TICKET;
  delete (config.parameters as Record<string, number>).FOLLOWUP_CAP;
  config.paramMeta = (config.paramMeta as { id: string }[]).filter(
    (p) => !['POINT_TICKET', 'FOLLOWUP_CAP'].includes(p.id),
  );
  const mode = {
    rules_version: 1,
    design_version: '0.18.0',
    schema_version: 16,
    content_hash: 'a'.repeat(64),
    config,
  };
  return {
    rules_version: mode.rules_version,
    design_version: mode.design_version,
    schema_version: mode.schema_version,
    content_hash: mode.content_hash,
    modes: { quick: mode, standard: { ...mode, config: { ...config, mode: 'standard' } } },
  };
}

for (const game of ['blackjack', 'bidding', 'likes', 'rps', 'linklink', 'fishing']) {
  test(`${game} exposes the appropriate random proof from its actual page`, async ({ page }) => {
    const errors = collectConsoleViolations(page);
    await mockRoleSession(page, 'user', 'user');
    await mockPublicConfig(page, 'user');
    await page.setViewportSize({ width: 390, height: 1000 });
    await page.emulateMedia({ reducedMotion: 'reduce', colorScheme: 'dark' });
    await page.addInitScript(() => localStorage.setItem('nb.lang', 'en'));
    await page.route('**/api/events', (route) =>
      route.fulfill({ status: 200, contentType: 'text/event-stream', body: ': ready\n\n' }),
    );
    const id =
      game === 'blackjack'
        ? tableID
        : game === 'bidding'
          ? biddingID
          : game === 'rps'
            ? rpsTestSessionID
            : (game === 'likes' ? 'lik_' : game === 'linklink' ? 'll_' : 'fb_') + 'A'.repeat(22);
    const proof = opening(game, id);
    const bid = biddingHomeWire();
    const likeState = {
      ...bid,
      current: {
        ...bid.current,
        id,
        game: 'likes',
        mode: 'quick',
        phase: 'plan',
        view: likes.initial,
      },
    };
    const board = {
      session_id: id,
      spec: '6x8',
      state: 'active',
      price: '3',
      revision: '1',
      board: {
        rows: 6,
        cols: 8,
        tiles: Array.from({ length: 48 }, (_, i) => ({
          row: Math.floor(i / 8),
          col: i % 8,
          tile_key: 'tile_' + String(Math.floor(i / 4) + 1).padStart(2, '0'),
          removed: false,
        })),
      },
      pairs_removed: 0,
      total_pairs: 24,
      started_at: 1800000000,
      deadline: 1800000150,
      server_now: 1800000010,
    };
    const fish = {
      batch_id: id,
      bait: 'worm',
      count: 1,
      unit_price: '2.5',
      entry_total: '2.5',
      rules_version: 1,
      payment: { general: '2.5', game: '0' },
      outcomes: [
        {
          ordinal: 0,
          species_key: 'koi',
          tier: 'legend',
          size_cm: 180,
          reward: '12',
          net_reward: '12',
          rake: { platform: '0', welfare: '0', thursday: '0' },
        },
      ],
      payout_total: '12',
      net_payout_total: '12',
      rake: { platform: '0', welfare: '0', thursday: '0' },
      balance: '14.5',
      game_balance: '0',
      settled_at: 1800000010,
      idempotent_replay: false,
    };
    await page.route('**/api/games**', async (route) => {
      const path = new URL(route.request().url()).pathname;
      let body: unknown;
      if (path === '/api/games') body = { ...gamesSnapshotWire(), tutorial_rps_seen: true };
      else if (path.includes('/randomness/'))
        body = { proof: game === 'fishing' ? { ...proof, seed } : proof };
      else if (path.endsWith('/lease')) body = { expires_at: 1800000035 };
      else if (game === 'rps' && path.endsWith('/leaderboard')) {
        const query = new URL(route.request().url()).searchParams;
        body = {
          mode: query.get('mode'),
          board: query.get('board'),
          window_days: 30,
          window_start: 1700000000,
          min_sessions: 10,
          rows: [],
          me: null,
        };
      } else if (game === 'fishing' && path.endsWith('/leaderboard')) {
        const board = new URL(route.request().url()).searchParams.get('board');
        body = {
          board,
          window_start: board === 'single' ? null : 1700000000,
          entries: [],
          me: null,
        };
      } else if (path === '/api/games/likes/catalog') body = catalog();
      else if (game === 'blackjack' && path.endsWith('/state')) body = blackjackWire();
      else if (game === 'bidding' && path.endsWith('/state')) body = bid;
      else if (game === 'likes' && path.endsWith('/state')) body = likeState;
      else if (game === 'rps' && path.endsWith('/state'))
        body = { kind: 'session', session: rpsStateWire() };
      else if (game === 'linklink' && path.endsWith('/session')) body = board;
      else if (game === 'fishing' && path.endsWith('/state'))
        body = { settlement_pending: null, unrevealed: fish, has_more_unrevealed: false };
      else {
        await route.fallback();
        return;
      }
      await route.fulfill({ json: body });
    });
    await page.goto(`${USER_ORIGIN}/games/${game}`);
    if (game === 'likes') await expect(page.locator('.likes-arena')).toBeVisible();
    await page.locator('.random-proof summary').focus();
    await page.keyboard.press('Enter');
    await expect(page.locator('.random-proof')).toContainText(proof.commitment);
    if (game === 'fishing')
      await expect(page.getByRole('button', { name: 'Verify locally' })).toBeVisible();
    else {
      await expect(page.getByRole('button', { name: 'Save commitment' })).toBeVisible();
      await expect(page.locator('.random-proof')).not.toContainText(seed);
    }
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    if (game === 'rps')
      expect((await page.locator('.rps-match__heading h2').boundingBox())!.width).toBeGreaterThan(
        120,
      );
    if (process.env.NONBIRI_RANDOM_SCREENSHOTS) {
      await mkdir(process.env.NONBIRI_RANDOM_SCREENSHOTS, { recursive: true });
      await page.screenshot({
        path: `${process.env.NONBIRI_RANDOM_SCREENSHOTS}/${game}-phone.png`,
        fullPage: true,
      });
    }
    errors.assertNone();
  });
}

for (const width of [390, 1440]) {
  test(`random proof ${width} saves opening, verifies result, survives reload and rejects tampering`, async ({
    page,
  }) => {
    const errors = collectConsoleViolations(page);
    await mockRoleSession(page, 'user', 'user');
    await mockPublicConfig(page, 'user');
    await page.setViewportSize({ width, height: 1000 });
    await page.emulateMedia({
      reducedMotion: width === 390 ? 'reduce' : 'no-preference',
      colorScheme: width === 390 ? 'dark' : 'light',
    });
    await page.addInitScript(() => localStorage.setItem('nb.lang', 'en'));
    let final = false,
      tampered = false;
    await page.route('**/api/games**', async (route) => {
      const path = new URL(route.request().url()).pathname;
      const proof = final
        ? { ...fixed, seed: tampered ? '00'.repeat(32) : seed }
        : { ...fixed, seed: undefined, streams: undefined };
      await route.fulfill({
        json:
          path === '/api/games'
            ? gamesSnapshotWire()
            : path.includes('/randomness/')
              ? { proof }
              : blackjackWire(final ? 'result' : 'decision'),
      });
    });
    await page.goto(`${USER_ORIGIN}/games/blackjack`);
    await page.locator('.random-proof summary').click();
    const download = page.waitForEvent('download');
    await page.getByRole('button', { name: 'Save commitment' }).click();
    const saved = await download,
      path = await saved.path();
    const opened = JSON.parse(await readFile(path!, 'utf8'));
    expect(opened.commitment).toBe(fixed.commitment);
    expect(opened.seed).toBeUndefined();
    final = true;
    await expect(page.getByRole('button', { name: 'Verify locally' })).toBeVisible({
      timeout: 10000,
    });
    await page.getByRole('button', { name: 'Verify locally' }).click();
    await expect(page.locator('.random-proof-status')).toContainText(
      'all 1 recorded draws reproduce',
    );
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
      true,
    );
    if (process.env.NONBIRI_RANDOM_SCREENSHOTS) {
      await mkdir(process.env.NONBIRI_RANDOM_SCREENSHOTS, { recursive: true });
      await page.screenshot({
        path: `${process.env.NONBIRI_RANDOM_SCREENSHOTS}/verified-${width}.png`,
        fullPage: true,
      });
    }
    await page.reload();
    await page.locator('.random-proof summary').click();
    await expect(page.getByRole('button', { name: 'Download proof' })).toBeVisible();
    await expect(page.locator('.random-proof-status')).toHaveText('');
    tampered = true;
    await page.reload();
    await page.locator('.random-proof summary').click();
    await page.getByRole('button', { name: 'Verify locally' }).click();
    await expect(page.locator('.random-proof-status')).toContainText('Verification failed');
    errors.assertNone();
  });
}
