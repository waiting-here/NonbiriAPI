import { randomUUID } from 'node:crypto';
import { mkdirSync, readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import {
  expect,
  test,
  type Browser,
  type BrowserContext,
  type Locator,
  type Page,
  type TestInfo,
} from '@playwright/test';
import commonEn from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import commonZh from '../../src/shared/i18n/common/zh.json' with { type: 'json' };
import userEn from '../../src/user/i18n/en.json' with { type: 'json' };
import type { KeyQueueConfig } from '../../src/shared/operations/keyQueue';

type Cookie = { Name: string; Value: string };
interface Fixture {
  admin_url: string;
  user_url: string;
  admin_cookie: Cookie;
  users: { id: string; cookie: Cookie }[];
  management: { automatic_donation_id: string };
}
type Limits = KeyQueueConfig & { max_concurrency: number; max_rpm: number };
type EndpointKey = Limits & { id: string; revision: string };
interface Donation {
  id: string;
  status: string;
  keys: { id: string; endpoint_id: string | null; endpoint_key_id: string | null }[];
}
const ownerCopy = commonEn.common.keyLimits;
const managerCopy = commonZh.common.keyLimits;
const charity = userEn.user.charity;

test.skip(
  process.env.NONBIRI_MANAGEMENT_BROWSER !== '1',
  'Requires the isolated management fixture and its real backend.',
);

async function session(browser: Browser, state: Fixture, admin: boolean) {
  const origin = admin ? state.admin_url : state.user_url;
  const cookie = admin ? state.admin_cookie : state.users[0].cookie;
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    reducedMotion: 'reduce',
  });
  const origins = new Set([state.admin_url, state.user_url]);
  await context.route('**/*', (route) =>
    origins.has(new URL(route.request().url()).origin)
      ? route.continue()
      : route.abort('blockedbyclient'),
  );
  await context.addCookies([
    {
      name: cookie.Name,
      value: cookie.Value,
      domain: new URL(origin).hostname,
      path: admin ? '/admin' : '/api',
      httpOnly: true,
      sameSite: 'Lax',
    },
  ]);
  await context.addInitScript(
    (language) => {
      localStorage.setItem('nb.lang', language);
      localStorage.setItem('nb.theme', 'light');
    },
    admin ? 'zh' : 'en',
  );
  return context;
}

async function read<T>(context: BrowserContext, origin: string, path: string): Promise<T> {
  const response = await context.request.get(origin + path, { headers: { Origin: origin } });
  expect(response.status(), path).toBe(200);
  return response.json() as Promise<T>;
}

async function visuals(page: Page, info: TestInfo, name: string, focus: Locator) {
  for (const theme of ['light', 'dark']) {
    await page.evaluate((value) => {
      document.documentElement.dataset.theme = value;
    }, theme);
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
      await focus.scrollIntoViewIfNeeded();
      await expect(focus).toBeInViewport();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      );
      expect(
        await focus.evaluate((element) => {
          const fieldset = element.closest('.nb-key-limits')!;
          const bounds = element.getBoundingClientRect();
          return (
            fieldset.scrollWidth <= fieldset.clientWidth + 1 &&
            bounds.left >= 0 &&
            bounds.right <= innerWidth
          );
        }),
      ).toBe(true);
      const filename = `${name}-${theme}-${width}.png`;
      const path = process.env.NONBIRI_VISUAL_DIR
        ? resolve(process.env.NONBIRI_VISUAL_DIR, filename)
        : info.outputPath(filename);
      mkdirSync(dirname(path), { recursive: true });
      await page.screenshot({ path, animations: 'disabled' });
    }
  }
}

test('donor edits shared queue limits, returns to the donation, and management reads the same key', async ({
  browser,
}, info) => {
  test.setTimeout(120_000);
  const state = JSON.parse(
    readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8'),
  ) as Fixture;
  const owner = await session(browser, state, false);
  const admin = await session(browser, state, true);
  let original: EndpointKey | undefined;
  let endpointID = '';
  let physicalKeyID = '';
  let changed = false;
  try {
    const donationID = state.management.automatic_donation_id;
    const donationPath = `/charity/donations/${donationID}`;
    const donation = await read<Donation>(owner, state.user_url, `/api/donations/${donationID}`);
    expect(donation.status).toBe('approved');
    const donatedKey = donation.keys[0];
    expect(donatedKey.endpoint_id).not.toBeNull();
    expect(donatedKey.endpoint_key_id).not.toBeNull();
    endpointID = donatedKey.endpoint_id!;
    physicalKeyID = donatedKey.endpoint_key_id!;
    const keysPath = `/api/endpoints/${endpointID}/keys?limit=100`;
    const currentKey = async () => {
      const keys = await read<{ data: EndpointKey[] }>(owner, state.user_url, keysPath);
      const key = keys.data.find((item) => item.id === physicalKeyID);
      expect(key).toBeDefined();
      return key!;
    };
    original = await currentKey();
    const page = await owner.newPage();
    const pageErrors: string[] = [];
    page.on('pageerror', (error) => pageErrors.push(error.message));
    await page.goto(state.user_url + donationPath);
    const donationURL = page.url();
    const openEditor = async () => {
      const link = page.getByRole('link', { name: charity.editSharedKeyLimits, exact: true });
      await expect(link).toHaveAttribute(
        'href',
        `/endpoints/${endpointID}?focus_key_id=${physicalKeyID}&edit_key=1`,
      );
      await link.click();
      await expect(page).toHaveURL(
        state.user_url + `/endpoints/${endpointID}?focus_key_id=${physicalKeyID}&edit_key=1`,
      );
      const editor = page.getByRole('region', { name: userEn.user.services.editKey, exact: true });
      await expect(editor).toBeVisible();
      return editor;
    };
    const saveAndReturn = async (editor: Locator, expected: Limits) => {
      const keyPath = `/api/endpoints/${endpointID}/keys/${physicalKeyID}`;
      const saved = page.waitForResponse(
        (response) =>
          response.request().method() === 'PATCH' && new URL(response.url()).pathname === keyPath,
      );
      changed = true;
      await editor
        .getByRole('button', { name: userEn.user.core['common.save'], exact: true })
        .click();
      const response = await saved;
      expect(response.status()).toBe(200);
      expect(await response.json()).toMatchObject({ ...expected });
      await expect(editor).toBeHidden();
      expect(await currentKey()).toMatchObject({ ...expected });
      await page.getByRole('link', { name: charity.backToDonation, exact: true }).click();
      await expect(page).toHaveURL(donationURL);
      await expect(
        page.getByRole('heading', { name: charity.donationDetailTitle, exact: true }),
      ).toBeVisible();
      await expect(
        page.getByRole('link', { name: charity.editSharedKeyLimits, exact: true }),
      ).toBeVisible();
    };
    const first: Limits = {
      max_concurrency: 2,
      max_rpm: 30,
      concurrency_overflow: 'queue',
      concurrency_queue_limit: 7,
      rpm_overflow: 'reject',
      rpm_queue_limit: original.rpm_queue_limit,
    };
    let editor = await openEditor();
    await editor.getByLabel(ownerCopy.concurrency, { exact: true }).fill('2');
    await editor.getByLabel(ownerCopy.rpm, { exact: true }).fill('30');
    await editor.getByLabel(ownerCopy.concurrencyOverflow, { exact: true }).selectOption('queue');
    await editor.getByLabel(ownerCopy.concurrencyQueueLimit, { exact: true }).fill('7');
    await editor.getByLabel(ownerCopy.rpmOverflow, { exact: true }).selectOption('reject');
    await expect(editor.getByLabel(ownerCopy.rpmQueueLimit, { exact: true })).toHaveCount(0);
    await visuals(
      page,
      info,
      'key-queue-owner-concurrency',
      editor.getByLabel(ownerCopy.concurrencyQueueLimit, { exact: true }),
    );
    await saveAndReturn(editor, first);

    editor = await openEditor();
    await expect(editor.getByLabel(ownerCopy.concurrencyQueueLimit, { exact: true })).toHaveValue(
      '7',
    );
    await editor.getByLabel(ownerCopy.concurrencyOverflow, { exact: true }).selectOption('reject');
    await editor.getByLabel(ownerCopy.rpmOverflow, { exact: true }).selectOption('queue');
    await editor.getByLabel(ownerCopy.rpmQueueLimit, { exact: true }).fill('11');
    await expect(editor.getByLabel(ownerCopy.concurrencyQueueLimit, { exact: true })).toHaveCount(
      0,
    );
    const final: Limits = {
      ...first,
      concurrency_overflow: 'reject',
      rpm_overflow: 'queue',
      rpm_queue_limit: 11,
    };
    await saveAndReturn(editor, final);

    const managed = await read<{ keys: (Limits & { id: string })[] }>(
      admin,
      state.admin_url,
      `/admin/api/donations/${donationID}`,
    );
    expect(managed.keys.find((item) => item.id === donatedKey.id)).toMatchObject({ ...final });
    const manager = await admin.newPage();
    manager.on('pageerror', (error) => pageErrors.push(error.message));
    await manager.goto(
      state.admin_url +
        `/charity?charity_section=donations&donation_id=${donationID}&donation_key=${donatedKey.id}`,
    );
    const limits = manager.locator('.donation-key-editor .nb-key-limits');
    await expect(limits).toHaveCount(1);
    await expect(limits.getByLabel(managerCopy.concurrency, { exact: true })).toHaveValue('2');
    await expect(limits.getByLabel(managerCopy.rpm, { exact: true })).toHaveValue('30');
    await expect(limits.getByLabel(managerCopy.concurrencyOverflow, { exact: true })).toHaveValue(
      'reject',
    );
    await expect(limits.getByLabel(managerCopy.rpmOverflow, { exact: true })).toHaveValue('queue');
    const queueLimit = limits.getByLabel(managerCopy.rpmQueueLimit, { exact: true });
    await expect(queueLimit).toHaveValue('11');
    await expect(queueLimit).toBeEnabled();
    await expect(limits.getByLabel(managerCopy.concurrencyQueueLimit, { exact: true })).toHaveCount(
      0,
    );
    await visuals(manager, info, 'key-queue-admin-rpm', queueLimit);
    expect(pageErrors).toEqual([]);
  } finally {
    try {
      if (changed && original) {
        const keys = await read<{ data: EndpointKey[] }>(
          owner,
          state.user_url,
          `/api/endpoints/${endpointID}/keys?limit=100`,
        );
        const current = keys.data.find((item) => item.id === physicalKeyID)!;
        const response = await owner.request.patch(
          state.user_url + `/api/endpoints/${endpointID}/keys/${physicalKeyID}`,
          {
            headers: { Origin: state.user_url, 'Idempotency-Key': randomUUID() },
            data: {
              expected_revision: current.revision,
              max_concurrency: original.max_concurrency,
              max_rpm: original.max_rpm,
              concurrency_overflow: original.concurrency_overflow,
              rpm_overflow: original.rpm_overflow,
              concurrency_queue_limit: original.concurrency_queue_limit,
              rpm_queue_limit: original.rpm_queue_limit,
            },
          },
        );
        expect(response.status(), 'Restore the isolated shared key settings').toBe(200);
      }
    } finally {
      await owner.close();
      await admin.close();
    }
  }
});
