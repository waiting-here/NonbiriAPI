import { gamesSnapshotWire } from '../../src/user/games/common/testFixtures';
import { USER_ORIGIN } from './ports';
import { collectConsoleViolations, mockJson, mockPublicConfig, mockRoleSession } from './support';
import { expect, test } from './test';

const cases = [
  { name: 'English light desktop', language: 'en', theme: 'light', width: 1_440 },
  { name: 'Chinese dark 820px boundary', language: 'zh', theme: 'dark', width: 820 },
  { name: 'English dark narrow mobile', language: 'en', theme: 'dark', width: 390 },
] as const;

for (const fixture of cases) {
  test(`game artwork is local, complete, and undistorted in ${fixture.name}`, async ({ page }) => {
    await page.setViewportSize({ width: fixture.width, height: 900 });
    await page.addInitScript(
      ({ language, theme }) => {
        localStorage.setItem('nb.lang', language);
        localStorage.setItem('nb.theme', theme);
      },
      fixture,
    );
    await mockPublicConfig(page, 'user');
    await mockRoleSession(page, 'user', 'user');
    await mockJson(page, {
      origin: USER_ORIGIN,
      method: 'GET',
      path: '/api/games',
      body: gamesSnapshotWire(),
    });

    const imageRequests: string[] = [];
    page.on('request', (request) => {
      if (request.resourceType() === 'image') imageRequests.push(request.url());
    });
    const consoleGuard = collectConsoleViolations(page);
    await page.goto(`${USER_ORIGIN}/games`);

    const cards = page.locator('.game-center-card');
    const heroes = cards.locator('.game-center-card__hero img.game-hero');
    await expect(cards).toHaveCount(3);
    await expect(heroes).toHaveCount(3);
    for (const hero of await heroes.all()) {
      await hero.scrollIntoViewIfNeeded();
      await expect(hero).toHaveJSProperty('complete', true);
    }

    const measurements = await heroes.evaluateAll((images) =>
      images.map((image) => {
        const element = image as HTMLImageElement;
        const box = element.getBoundingClientRect();
        const heroBox = element.parentElement?.getBoundingClientRect();
        const cardBox = element.closest('.game-center-card')?.getBoundingClientRect();
        const bodyBox = element
          .closest('.game-center-card')
          ?.querySelector('.game-center-card__body')
          ?.getBoundingClientRect();
        return {
          alt: element.alt,
          ariaHidden: element.getAttribute('aria-hidden'),
          naturalWidth: element.naturalWidth,
          naturalHeight: element.naturalHeight,
          objectFit: getComputedStyle(element).objectFit,
          ratio: box.width / box.height,
          sameHeroBounds:
            heroBox !== undefined &&
            Math.abs(box.x - heroBox.x) < 0.5 &&
            Math.abs(box.y - heroBox.y) < 0.5 &&
            Math.abs(box.width - heroBox.width) < 0.5 &&
            Math.abs(box.height - heroBox.height) < 0.5,
          containedByCard:
            cardBox !== undefined &&
            box.left >= cardBox.left - 0.5 &&
            box.right <= cardBox.right + 0.5 &&
            box.top >= cardBox.top - 0.5 &&
            box.bottom <= cardBox.bottom + 0.5,
          aboveBody: bodyBox !== undefined && box.bottom <= bodyBox.top + 0.5,
        };
      }),
    );

    expect(measurements).toHaveLength(3);
    for (const measurement of measurements) {
      expect(measurement).toMatchObject({
        alt: '',
        ariaHidden: 'true',
        naturalWidth: 960,
        naturalHeight: 480,
        objectFit: 'cover',
        sameHeroBounds: true,
        containedByCard: true,
        aboveBody: true,
      });
      expect(measurement.ratio).toBeCloseTo(2, 2);
    }

    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true);
    const cardBoxes = await cards.evaluateAll((elements) =>
      elements.map((element) => element.getBoundingClientRect().toJSON()),
    );
    if (fixture.width <= 820) {
      expect(new Set(cardBoxes.map((box) => Math.round(box.x))).size).toBe(1);
    } else {
      expect(new Set(cardBoxes.map((box) => Math.round(box.x))).size).toBe(3);
    }

    for (const rawURL of imageRequests) {
      const imageURL = new URL(rawURL);
      expect(imageURL.origin).toBe(USER_ORIGIN);
    }
    const artworkRequests = imageRequests.filter((rawURL) => rawURL.endsWith('.webp'));
    expect(artworkRequests).toHaveLength(3);
    for (const rawURL of artworkRequests) {
      const imageURL = new URL(rawURL);
      expect(imageURL.pathname).toMatch(/^\/assets\/(fishing|linklink|rps)-[A-Za-z0-9_-]+\.webp$/);
    }
    consoleGuard.assertNone();
  });
}
