import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { expect, test, type Page } from './test';
import { USER_ORIGIN } from './ports';
import { creditDisplayFixtures } from './credit-display-fixtures';
import { pixelContrastSamples } from './appearance-contrast';
import { gamesSnapshotWire } from '../../src/user/games/common/testFixtures';
import { blackjackWire } from '../../src/user/games/blackjack/testFixtures';
import { LAST_TICK, newGame, type Phrase } from '../../src/user/games/steady-catch/engine';
import type { Session } from '../../src/user/games/steady-catch/session';
import { gachaSnapshot } from '../fixtures/gacha';

type Locator = ReturnType<Page['locator']>;

const phrases = JSON.parse(
  readFileSync('../internal/game/steadycatch/engine/phrases.json', 'utf8'),
) as Phrase[];

async function recordFractions(page: Page, label: string) {
  await page.evaluate(() => document.fonts.ready);
  const samples = (await pixelContrastSamples(page)).filter((sample) =>
    sample.selector.includes('.credit-number__fraction'),
  );
  const directory = process.env.NONBIRI_CREDIT_VISUAL_DIR;
  if (directory) {
    mkdirSync(directory, { recursive: true });
    await page.screenshot({ path: resolve(directory, label + '.png') });
    writeFileSync(resolve(directory, label + '.json'), JSON.stringify(samples, null, 2));
  }
  expect(samples.length, label + ' actual fraction samples').toBeGreaterThan(0);
  expect
    .soft(
      samples.filter((sample) => sample.ratio < 4.5),
      label,
    )
    .toEqual([]);
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
}

async function controlContrast(page: Page, controls: Locator, label: string) {
  const rendered = await controls.evaluateAll((elements) =>
    elements.map((element) => {
      const context = document.createElement('canvas').getContext('2d')!;
      const rgb = (color: string) => {
        context.clearRect(0, 0, 1, 1);
        context.fillStyle = color;
        context.fillRect(0, 0, 1, 1);
        return [...context.getImageData(0, 0, 1, 1).data];
      };
      const linear = (value: number) =>
        value / 255 <= 0.04045 ? value / 255 / 12.92 : ((value / 255 + 0.055) / 1.055) ** 2.4;
      const luminance = (color: number[]) =>
        0.2126 * linear(color[0]) + 0.7152 * linear(color[1]) + 0.0722 * linear(color[2]);
      const style = getComputedStyle(element);
      let surface: Element | null = element;
      let background = rgb(style.backgroundColor);
      const images = [style.backgroundImage];
      while (background[3] === 0 && surface.parentElement) {
        surface = surface.parentElement;
        const paint = getComputedStyle(surface);
        background = rgb(paint.backgroundColor);
        images.push(paint.backgroundImage);
      }
      const icon = element.querySelector('svg');
      const foreground = rgb(icon ? getComputedStyle(icon).color : style.color);
      const a = luminance(foreground),
        b = luminance(background);
      const texts: string[] = [];
      const walker = document.createTreeWalker(element, NodeFilter.SHOW_TEXT);
      for (let node = walker.nextNode(); node; node = walker.nextNode()) {
        const range = document.createRange();
        range.selectNodeContents(node);
        if (
          node.parentElement?.checkVisibility() &&
          [...range.getClientRects()].some((r) => r.width && r.height)
        )
          if (node.textContent?.trim()) texts.push(node.textContent.trim());
      }
      let opacity = 1;
      for (let parent: Element | null = icon ?? element; parent; parent = parent.parentElement)
        opacity *= Number(getComputedStyle(parent).opacity);
      return {
        name: element.getAttribute('aria-label') ?? element.textContent?.trim(),
        background,
        foreground,
        image: images.find((value) => value !== 'none') ?? 'none',
        opacity,
        icon: Boolean(icon),
        texts,
        ratio: (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05),
      };
    }),
  );
  expect(rendered.length, label).toBeGreaterThan(0);
  // These controls paint opaque solid surfaces, so the rendered CSS pair is exact.
  expect
    .soft(
      rendered.filter(
        (s) =>
          s.background[3] !== 255 ||
          s.foreground[3] !== 255 ||
          s.image !== 'none' ||
          s.opacity !== 1,
      ),
      label + ' solid controls',
    )
    .toEqual([]);
  expect
    .soft(
      rendered.filter((s) => s.icon && s.ratio < 3),
      label + ' necessary icons',
    )
    .toEqual([]);
  const names = rendered.flatMap((sample) => sample.texts);
  const text = (await pixelContrastSamples(page)).filter((sample) => names.includes(sample.text));
  for (const name of names)
    expect
      .soft(
        text.some((sample) => sample.text === name),
        label + ' visible text ' + name,
      )
      .toBe(true);
  expect
    .soft(
      text.filter((sample) => sample.ratio < 4.5),
      label + ' text',
    )
    .toEqual([]);
  const directory = process.env.NONBIRI_CREDIT_VISUAL_DIR;
  if (directory) {
    mkdirSync(directory, { recursive: true });
    writeFileSync(resolve(directory, label + '.json'), JSON.stringify({ rendered, text }, null, 2));
    await page.screenshot({ path: resolve(directory, label + '.png') });
  }
}

async function catchTools(page: Page, label: string) {
  const toolbar = page.locator('.catch-game .game-toolbar');
  await toolbar.scrollIntoViewIfNeeded();
  const controls = toolbar.locator('.game-toolbar__button');
  const visibility = await controls.evaluateAll((elements) =>
    elements.map((element) => {
      const rect = element.getBoundingClientRect();
      let left = 0,
        right = innerWidth;
      const scrolling = [];
      for (let parent = element.parentElement; parent; parent = parent.parentElement) {
        const css = getComputedStyle(parent),
          bounds = parent.getBoundingClientRect();
        if (/hidden|clip|auto|scroll/.test(css.overflowX)) {
          left = Math.max(left, bounds.left);
          right = Math.min(right, bounds.right);
          scrolling.push({
            class: parent.className,
            overflow: css.overflowX,
            width: parent.clientWidth,
            scrollWidth: parent.scrollWidth,
          });
        }
      }
      return {
        name: element.getAttribute('aria-label'),
        left: rect.left,
        right: rect.right,
        clipLeft: left,
        clipRight: right,
        scrolling,
        complete: rect.left >= left - 0.5 && rect.right <= right + 0.5,
      };
    }),
  );
  expect
    .soft(
      visibility.filter((item) => !item.complete),
      'complete tools ' + label,
    )
    .toEqual([]);
  const directory = process.env.NONBIRI_CREDIT_VISUAL_DIR;
  if (directory)
    writeFileSync(
      resolve(directory, 'catch-tools-' + label + '-bounds.json'),
      JSON.stringify(visibility, null, 2),
    );
  await controlContrast(page, controls, 'catch-tools-' + label + '-default');
  const rules = toolbar.getByRole('button', { name: 'How to play', exact: true });
  await rules.hover();
  await controlContrast(page, rules, 'catch-tools-' + label + '-hover');
  const sound = toolbar.getByRole('button', { name: 'Sound', exact: true });
  await sound.click();
  let item = page.getByRole('menuitemcheckbox');
  if ((await item.getAttribute('aria-checked')) !== 'true') {
    await item.click();
    await sound.click();
    item = page.getByRole('menuitemcheckbox');
  }
  await expect(item).toHaveAttribute('aria-checked', 'true');
  await item.hover();
  await controlContrast(page, item, 'catch-tools-' + label + '-selected');
  await page.keyboard.press('Escape');
  await toolbar.getByRole('button', { name: 'More', exact: true }).click();
  const phrases = page.getByRole('menuitem', { name: 'Phrases', exact: true });
  await expect(phrases).toBeVisible();
  await phrases.click();
  const catalog = page.getByRole('dialog', { name: 'Phrase catalog', exact: true });
  await expect(catalog).toBeVisible();
  await catalog.getByRole('button', { name: 'Close', exact: true }).click();
  await expect(catalog).not.toBeVisible();
  await rules.click();
  const help = page.getByRole('dialog', { name: 'How to catch steadily', exact: true });
  await expect(help).toBeVisible();
  const close = help.getByRole('button', { name: 'Close', exact: true });
  await controlContrast(page, close, 'catch-tools-' + label + '-close');
  await close.hover();
  await controlContrast(page, close, 'catch-tools-' + label + '-close-hover');
  await close.click();
  await expect(help).not.toBeVisible();
  await page.getByRole('button', { name: 'Catch again', exact: true }).scrollIntoViewIfNeeded();
  await expect(page.getByRole('button', { name: 'Catch again', exact: true })).toBeEnabled();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
}

for (const theme of ['light', 'dark']) {
  for (const width of [1440, 390]) {
    test(`credit fixed game surfaces ${theme} ${width}`, async ({ page }) => {
      await page.setViewportSize({ width, height: 1000 });
      await page.emulateMedia({ reducedMotion: 'reduce' });
      await page.addInitScript((mode) => {
        localStorage.setItem('nb.lang', 'en');
        localStorage.setItem('nb.theme', mode);
        localStorage.setItem(
          'nb.gacha.preferences',
          JSON.stringify({
            music: false,
            sound: false,
            reduced: true,
          }),
        );
      }, theme);
      await creditDisplayFixtures(page, 'default');
      const snapshot = gamesSnapshotWire();
      snapshot.balance = '0.001';
      snapshot.game_balance = '0.001';
      snapshot.steadycatch.enabled = true;
      snapshot.steadycatch.price = '0.001';
      snapshot.steadycatch.first_clear_reward = '0.001';
      snapshot.bidding.enabled = true;
      for (const mode of Object.values(snapshot.bidding.modes)) {
        mode.enabled = true;
        mode.ticket = '0.001';
      }
      const blackjack = blackjackWire();
      if (blackjack.you) {
        blackjack.you.stake = '0.001';
        blackjack.you.payment = { general: '0.001', game: '0' };
      }
      for (const seat of blackjack.table.fact.seats) seat.stake = '0.001';
      const result: Session = {
        id: 'sc_' + 'A'.repeat(22),
        status: 'completed',
        revision: 1,
        state: { ...newGame(123), tick: LAST_TICK, cause: 'time', score: 700 },
        payment: { general: '0.001', game: '0' },
        first_clear_reward: '0.001',
        first_clear: true,
        reward: '0.001',
        created_at: 1800000000,
        expires_at: 1800001800,
        terminal_at: 1800000090,
        server_ms: 1800000090000,
      };
      await page.route('**/api/games**', (route) => {
        const path = new URL(route.request().url()).pathname;
        if (path === '/api/games') return route.fulfill({ json: snapshot });
        if (path.endsWith('/blackjack/state')) return route.fulfill({ json: blackjack });
        if (path.endsWith('/bidding/state'))
          return route.fulfill({
            json: { server_now: 1800000000, queue: null, current: null, latest_result: null },
          });
        if (path.endsWith('/steady-catch/catalog')) return route.fulfill({ json: phrases });
        if (path.endsWith('/steady-catch/session')) return route.fulfill({ json: result });
        if (path.endsWith('/steady-catch/leaderboard'))
          return route.fulfill({ json: { rows: [], me: null, window: '7d', as_of: 1800000000 } });
        return route.fallback();
      });
      const gacha = gachaSnapshot();
      gacha.credits = { general: '0.001', game: '0.001' };
      gacha.shop.permanent_price = '0.001';
      await page.route('**/api/gacha/state', (route) => route.fulfill({ json: gacha }));
      await page.route('**/api/gacha/actions', (route) => {
        expect(route.request().postDataJSON()).toMatchObject({ action: 'enter' });
        return route.fulfill({ json: { snapshot: gacha } });
      });
      await page.route('**/api/gacha/entry', (route) =>
        route.fulfill({ json: { enabled: true, entered: true, pools: [] } }),
      );
      for (const [path, selector] of [
        ['/games/blackjack', '.bj-board'],
        ['/games/bidding', '.bidding-game .bid-modes'],
        ['/games/steady-catch', '.catch-result .first-clear-badge'],
        ['/activities/gacha', '.gacha-app'],
      ]) {
        await page.goto(USER_ORIGIN + path);
        await expect(page.locator(selector).first()).toBeVisible();
        if (path === '/activities/gacha') {
          await page.getByTitle('Buy standard tickets', { exact: true }).click();
          await expect(page.getByRole('dialog')).toBeVisible();
        } else {
          await page.locator(selector).first().scrollIntoViewIfNeeded();
        }
        await expect(page.locator(selector).first()).toContainText('0.001');
        const wrappedAmounts = await page.locator('.credit-number').evaluateAll((elements) =>
          elements
            .filter((element) => element.checkVisibility() && element.textContent === '0.001')
            .map((element) => {
              const tops: number[] = [];
              const walker = document.createTreeWalker(element, NodeFilter.SHOW_TEXT);
              for (let node = walker.nextNode(); node; node = walker.nextNode()) {
                const range = document.createRange();
                range.selectNodeContents(node);
                tops.push(...[...range.getClientRects()].map((rect) => Math.round(rect.top)));
              }
              return { parent: element.parentElement?.className, lines: [...new Set(tops)] };
            })
            .filter((amount) => amount.lines.length > 1),
        );
        expect.soft(wrappedAmounts, path + ' ordinary 0.001 stays on one line').toEqual([]);
        await recordFractions(page, `credit-${path.split('/').pop()}-${theme}-${width}`);
        if (path === '/games/steady-catch') await catchTools(page, `${theme}-${width}`);
      }
    });
  }

  test(`credit extreme exact balance wraps at 390 in ${theme}`, async ({ page }) => {
    const balance = '170141183460469231731687303715884105.727';
    await page.setViewportSize({ width: 390, height: 1000 });
    await page.addInitScript((mode) => {
      localStorage.setItem('nb.lang', 'en');
      localStorage.setItem('nb.theme', mode);
    }, theme);
    await creditDisplayFixtures(page, 'default', balance);
    for (const [path, selector] of [
      ['/', '.home-wallet'],
      ['/games/fishing', '.game-wallets'],
    ]) {
      await page.goto(USER_ORIGIN + path);
      const regions = page.locator(selector);
      await expect(regions).not.toHaveCount(0);
      for (let index = 0; index < (await regions.count()); index++) {
        const region = regions.nth(index);
        await expect(region).toBeVisible();
        const amount = region.locator('.credit-number').filter({ hasText: '170,141' });
        await expect(amount).toHaveText('170,141,183,460,469,231,731,687,303,715,884,105.727');
        await amount.scrollIntoViewIfNeeded();
        const bounds = await amount.evaluate((element) => {
          const range = document.createRange();
          range.selectNodeContents(element);
          return [...range.getClientRects()].map((rect) => ({
            left: rect.left,
            right: rect.right,
          }));
        });
        expect(bounds.every((rect) => rect.left >= 0 && rect.right <= 391)).toBe(true);
        await recordFractions(
          page,
          `credit-extreme-${path === '/' ? 'home' : 'game'}-${theme}-390-wallet-${index}`,
        );
      }
    }
  });
}
