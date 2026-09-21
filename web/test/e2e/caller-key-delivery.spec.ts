import { resolve } from 'node:path';
import { USER_ORIGIN } from './ports';
import {
  assertNoSensitiveBrowserPersistence,
  collectConsoleViolations,
  installURLPersistenceObserver,
  mockJson,
  mockPublicConfig,
  mockRoleSession,
} from './support';
import { expect, test, type Page } from './test';

type BrowserContext = ReturnType<Page['context']>;

type CallerKeyMetadata = {
  display: string;
  created_at: number;
  updated_at: number;
  generation: string;
};

type CallerKeyResponse = {
  status: number;
  body: unknown;
  generation: string;
};

type CallerKeyPostMode = 'success' | 'abort' | 'defer-abort' | 'defer-invalid';

type Deferred<T> = {
  promise: Promise<T>;
  resolve: (value: T) => void;
};

interface CallerKeyServer {
  generation: string;
  metadata: CallerKeyMetadata | null;
  getCount: number;
  getGenerations: string[];
  posts: Array<{ body: unknown; headers: Record<string, string> }>;
  postMode: CallerKeyPostMode;
  postSecret?: string;
  postMetadata?: CallerKeyMetadata;
  postGate?: Deferred<void>;
  getResponse?: (count: number, server: CallerKeyServer) => CallerKeyResponse;
}

const JSON_HEADERS = {
  'content-type': 'application/json',
  'cache-control': 'no-store',
};

function deferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((settle) => {
    resolve = settle;
  });
  return { promise, resolve };
}

function metadata(generation: string, marker: string): CallerKeyMetadata {
  return {
    display: `nbk_${marker}…${marker}`,
    created_at: 1_700_000_000,
    updated_at: 1_700_000_001,
    generation,
  };
}

function successServer(
  generation: string,
  currentMetadata: CallerKeyMetadata | null,
  secret: string,
  nextMetadata: CallerKeyMetadata,
): CallerKeyServer {
  return {
    generation,
    metadata: currentMetadata,
    getCount: 0,
    getGenerations: [],
    posts: [],
    postMode: 'success',
    postSecret: secret,
    postMetadata: nextMetadata,
  };
}

async function fulfillJson(
  route: Parameters<Parameters<Page['route']>[1]>[0],
  response: CallerKeyResponse,
): Promise<void> {
  await route.fulfill({
    status: response.status,
    headers: {
      ...JSON_HEADERS,
      'X-Nonbiri-CallerKey-Generation': response.generation,
    },
    body: JSON.stringify(response.body),
  });
}

async function installCallerKeyServer(page: Page, server: CallerKeyServer): Promise<void> {
  await page.route('**/*', async (route) => {
    const request = route.request();
    const requestURL = new URL(request.url());
    if (requestURL.origin !== USER_ORIGIN) {
      await route.fallback();
      return;
    }

    if (request.method() === 'GET' && requestURL.pathname === '/api/caller-key') {
      server.getCount += 1;
      const response = server.getResponse?.(server.getCount, server) ?? {
        status: 200,
        body: server.metadata,
        generation: server.generation,
      };
      server.getGenerations.push(response.generation);
      await fulfillJson(route, response);
      return;
    }

    if (
      request.method() === 'POST' &&
      requestURL.pathname === '/api/caller-key/regenerate' &&
      requestURL.search === ''
    ) {
      const rawBody = request.postData();
      let body: unknown = rawBody;
      try {
        body = rawBody === null ? undefined : JSON.parse(rawBody);
      } catch {
        // Keep malformed synthetic request bodies visible to the assertion.
      }
      server.posts.push({ body, headers: request.headers() });

      if (server.postMode === 'abort') {
        await route.abort('failed');
        return;
      }
      if (server.postMode === 'defer-abort' || server.postMode === 'defer-invalid') {
        if (!server.postGate) throw new Error('A deferred CallerKey response needs a gate.');
        await server.postGate.promise;
        if (server.postMode === 'defer-abort') {
          await route.abort('failed');
        } else {
          await fulfillJson(route, { status: 200, body: {}, generation: server.generation });
        }
        return;
      }

      if (!server.postSecret || !server.postMetadata)
        throw new Error('A successful CallerKey fixture needs a secret and metadata.');
      server.generation = server.postMetadata.generation;
      server.metadata = server.postMetadata;
      await fulfillJson(route, {
        status: 200,
        generation: server.generation,
        body: { secret: server.postSecret, metadata: server.postMetadata },
      });
      return;
    }

    await route.fallback();
  });
}

async function prepareUser(
  context: BrowserContext,
  page: Page,
  options: {
    tokens: string[];
    locale: 'en' | 'zh';
    theme: 'light' | 'dark';
    width: number;
    installObserver?: boolean;
    mockLogout?: boolean;
  },
): Promise<ReturnType<typeof collectConsoleViolations>> {
  const guard = collectConsoleViolations(page);
  if (options.installObserver !== false)
    await installURLPersistenceObserver(context, options.tokens);
  await page.setViewportSize({ width: options.width, height: 1_000 });
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.addInitScript(
    ({ locale, theme }) => {
      localStorage.setItem('nb.lang', locale);
      localStorage.setItem('nb.theme', theme);
    },
    { locale: options.locale, theme: options.theme },
  );
  await mockPublicConfig(page, 'user');
  await mockRoleSession(page, 'user', 'user');
  if (options.mockLogout) {
    await mockJson(page, {
      origin: USER_ORIGIN,
      method: 'POST',
      path: '/api/auth/logout',
      body: {},
    });
  }
  return guard;
}

async function screenshot(page: Page, name: string): Promise<void> {
  const directory = process.env.NONBIRI_VISUAL_DIR;
  if (!directory) return;
  await page.screenshot({ path: resolve(directory, `${name}.png`), fullPage: true });
}

async function assertFitsViewport(page: Page): Promise<void> {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
}

test('creates one-time CallerKey on the real keys route and copies it after Clipboard resolves', async ({
  context,
  page,
}) => {
  const secret = `nbk_${'A'.repeat(43)}`;
  const next = metadata('1', 'AAAA');
  const server = successServer('0', null, secret, next);
  const guard = await prepareUser(context, page, {
    tokens: [secret],
    locale: 'en',
    theme: 'light',
    width: 320,
  });
  await installCallerKeyServer(page, server);
  await page.goto(`${USER_ORIGIN}/keys`);

  await expect(page.getByRole('heading', { name: 'Global API access', exact: true })).toBeVisible();
  await expect(page.getByText('No account API key', { exact: true })).toBeVisible();
  await expect.poll(() => server.getCount).toBeGreaterThan(0);
  expect(server.getGenerations[0]).toBe('0');
  expect(secret).toMatch(/^nbk_[A-Za-z0-9_-]{43}$/);

  await page.getByRole('button', { name: 'Create API key', exact: true }).click();
  await expect(page.locator('.core-secret-value')).toHaveText(secret);
  expect(server.posts).toHaveLength(1);
  expect(server.posts[0].body).toEqual({ expected_generation: '0' });
  expect(server.posts[0].headers['idempotency-key']).toBeUndefined();
  await expect(page.getByText(next.display, { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: /copy.*identifier/i })).toHaveCount(0);

  await context.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: USER_ORIGIN });
  await page.getByRole('button', { name: 'Copy', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Copied', exact: true })).toBeVisible();
  await expect.poll(() => page.evaluate(() => navigator.clipboard?.readText())).toBe(secret);
  await assertFitsViewport(page);
  await assertNoSensitiveBrowserPersistence(page, [secret]);
  await screenshot(page, 'caller-key-320-en-light');

  await page.getByRole('button', { name: 'I have saved it — close', exact: true }).click();
  await expect(page.getByText(secret, { exact: true })).toHaveCount(0);
  guard.assertNone();
});

test('waits for Clipboard write completion before reporting a successful copy', async ({
  context,
  page,
}) => {
  const secret = `nbk_${'J'.repeat(42)}Q`;
  const server = successServer('0', null, secret, metadata('1', 'JJJJ'));
  const guard = await prepareUser(context, page, {
    tokens: [secret],
    locale: 'en',
    theme: 'light',
    width: 390,
  });
  await installCallerKeyServer(page, server);
  await page.goto(`${USER_ORIGIN}/keys`);
  await page.getByRole('button', { name: 'Create API key', exact: true }).click();
  await expect(page.getByText(secret, { exact: true })).toBeVisible();

  const clipboardWrite = deferred<void>();
  let writeArgument: string | undefined;
  await page.exposeFunction('__recordCallerKeyClipboardWrite', (value: string) => {
    writeArgument = value;
    return clipboardWrite.promise;
  });
  await page.evaluate(() => {
    const testWindow = window as Window & {
      __recordCallerKeyClipboardWrite?: (value: string) => Promise<void>;
    };
    if (!testWindow.__recordCallerKeyClipboardWrite)
      throw new Error('The synthetic clipboard binding was not installed.');
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: {
        writeText: (value: string) => testWindow.__recordCallerKeyClipboardWrite!(value),
      },
    });
  });

  await page.getByRole('button', { name: 'Copy', exact: true }).click();
  await expect.poll(() => writeArgument).toBe(secret);
  await expect(page.getByRole('button', { name: 'Copied', exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Copy', exact: true })).toBeVisible();

  clipboardWrite.resolve(undefined);
  await expect(page.getByRole('button', { name: 'Copied', exact: true })).toBeVisible();
  await assertNoSensitiveBrowserPersistence(page, [secret]);
  guard.assertNone();
});

test('keeps the full value selectable after Clipboard rejection on Chinese dark mobile', async ({
  context,
  page,
}) => {
  const secret = `nbk_${'B'.repeat(42)}Q`;
  const server = successServer('0', null, secret, metadata('1', 'BBBB'));
  const guard = await prepareUser(context, page, {
    tokens: [secret],
    locale: 'zh',
    theme: 'dark',
    width: 390,
  });
  await installCallerKeyServer(page, server);
  await page.goto(`${USER_ORIGIN}/keys`);
  await expect(page.getByText('尚未创建账户 API 密钥', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: '创建 API 密钥', exact: true }).click();
  await expect.poll(() => server.posts.length).toBe(1);
  await expect(page.locator('.core-secret-value')).toHaveText(secret);

  await page.evaluate(() => {
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText: () => Promise.reject(new Error('synthetic clipboard denial')) },
    });
  });
  await page.getByRole('button', { name: '复制', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('复制失败，请手动选择并复制。');
  await expect(page.getByText(secret, { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: '已复制', exact: true })).toHaveCount(0);
  await assertFitsViewport(page);
  await assertNoSensitiveBrowserPersistence(page, [secret]);
  await screenshot(page, 'caller-key-390-zh-dark');
  guard.assertNone();
});

test('requires confirmation for replacement and never recovers a closed secret after refresh', async ({
  context,
  page,
}) => {
  const secret = `nbk_${'C'.repeat(42)}8`;
  const current = metadata('7', 'CCCC');
  const next = metadata('8', 'DDDD');
  const server = successServer('7', current, secret, next);
  const guard = await prepareUser(context, page, {
    tokens: [secret],
    locale: 'en',
    theme: 'light',
    width: 390,
  });
  await installCallerKeyServer(page, server);
  await page.goto(`${USER_ORIGIN}/keys`);
  await expect(page.getByText(current.display, { exact: true })).toBeVisible();
  expect(server.getGenerations[0]).toBe('7');

  await page.getByRole('button', { name: 'Replace API key', exact: true }).click();
  const dialog = page.getByRole('alertdialog');
  await expect(dialog).toBeVisible();
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(dialog).toHaveCount(0);
  expect(server.posts).toHaveLength(0);

  await page.getByRole('button', { name: 'Replace API key', exact: true }).click();
  await page
    .getByRole('alertdialog')
    .getByRole('button', { name: 'Replace API key', exact: true })
    .click();
  await expect(page.getByText(secret, { exact: true })).toBeVisible();
  expect(server.posts).toHaveLength(1);
  expect(server.posts[0].body).toEqual({ expected_generation: '7' });
  await expect(page.getByText(next.display, { exact: true })).toBeVisible();
  await context.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: USER_ORIGIN });
  await page.getByRole('button', { name: 'Copy', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Copied', exact: true })).toBeVisible();
  await expect.poll(() => page.evaluate(() => navigator.clipboard?.readText())).toBe(secret);
  await assertNoSensitiveBrowserPersistence(page, [secret]);

  await page.getByRole('button', { name: 'I have saved it — close', exact: true }).click();
  await expect(page.getByText(secret, { exact: true })).toHaveCount(0);
  await page.reload();
  await expect(page.getByText(next.display, { exact: true })).toBeVisible();
  await expect(page.getByText(secret, { exact: true })).toHaveCount(0);
  await expect(page.locator('.core-secret-value')).toHaveCount(0);
  await assertNoSensitiveBrowserPersistence(page, [secret]);
  guard.assertNone();
});

test('keeps the one-time value visible when the post-generation metadata refresh fails', async ({
  context,
  page,
}) => {
  const secret = `nbk_${'D'.repeat(42)}Q`;
  const next = metadata('1', 'EEEE');
  const server = successServer('0', null, secret, next);
  server.getResponse = (count, current) =>
    count === 1
      ? { status: 200, body: null, generation: '0' }
      : {
          status: 200,
          body: {},
          generation: current.generation,
        };
  const guard = await prepareUser(context, page, {
    tokens: [secret],
    locale: 'en',
    theme: 'light',
    width: 390,
  });
  await installCallerKeyServer(page, server);
  await page.goto(`${USER_ORIGIN}/keys`);
  await page.getByRole('button', { name: 'Create API key', exact: true }).click();
  await expect(page.getByText(secret, { exact: true })).toBeVisible();
  await expect(
    page.getByText(
      'Key details could not be refreshed. The full key remains visible until you close it.',
      { exact: true },
    ),
  ).toBeVisible();
  await expect(page.getByText(secret, { exact: true })).toBeVisible();
  expect(server.posts).toHaveLength(1);
  await assertNoSensitiveBrowserPersistence(page, [secret]);
  await page.getByRole('button', { name: 'I have saved it — close', exact: true }).click();
  await expect(page.getByText(secret, { exact: true })).toHaveCount(0);
  guard.assertNone();
});

test('does not resend a lost generation response and asks for confirmation before another attempt', async ({
  context,
  page,
}) => {
  const marker = 'caller-key-lost-response-marker';
  const current = metadata('2', 'FFFF');
  const postGate = deferred<void>();
  const server: CallerKeyServer = {
    generation: '2',
    metadata: current,
    getCount: 0,
    getGenerations: [],
    posts: [],
    postMode: 'defer-invalid',
    postGate,
  };
  const guard = await prepareUser(context, page, {
    tokens: [marker],
    locale: 'en',
    theme: 'light',
    width: 390,
  });
  await installCallerKeyServer(page, server);
  await page.goto(`${USER_ORIGIN}/keys`);
  await expect(page.getByText(current.display, { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Replace API key', exact: true }).click();
  await page
    .getByRole('alertdialog')
    .getByRole('button', { name: 'Replace API key', exact: true })
    .click();
  await expect.poll(() => server.posts.length).toBe(1);
  postGate.resolve(undefined);
  await expect(
    page.getByText(
      'The response was lost. The page is checking the latest status and will not resend the action.',
      { exact: true },
    ),
  ).toBeVisible();
  expect(server.posts).toHaveLength(1);

  await page.getByRole('alertdialog').getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect.poll(() => server.getCount).toBeGreaterThan(1);
  await page.goto(`${USER_ORIGIN}/`);
  await page.goto(`${USER_ORIGIN}/keys`);
  await expect(page.getByText(current.display, { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Replace API key', exact: true })).toBeEnabled();
  await page.getByRole('button', { name: 'Replace API key', exact: true }).click();
  await expect(page.getByRole('alertdialog')).toBeVisible();
  expect(server.posts).toHaveLength(1);
  await page.getByRole('alertdialog').getByRole('button', { name: 'Cancel', exact: true }).click();
  await assertNoSensitiveBrowserPersistence(page, [marker]);
  guard.assertNone();
});

test('reads a higher CallerKey generation after another page advances the authority', async ({
  context,
  page,
}) => {
  const firstSecret = `nbk_${'G'.repeat(42)}Q`;
  const secondSecret = `nbk_${'H'.repeat(42)}Q`;
  const first = metadata('3', 'GGGG');
  const second = metadata('4', 'HHHH');
  const server: CallerKeyServer = {
    generation: '2',
    metadata: null,
    getCount: 0,
    getGenerations: [],
    posts: [],
    postMode: 'success',
    postSecret: firstSecret,
    postMetadata: first,
  };
  const guard = await prepareUser(context, page, {
    tokens: [firstSecret, secondSecret],
    locale: 'en',
    theme: 'light',
    width: 390,
  });
  await installCallerKeyServer(page, server);
  await page.goto(`${USER_ORIGIN}/keys`);
  await expect(page.getByText('No account API key', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Create API key', exact: true }).click();
  await expect(page.getByText(firstSecret, { exact: true })).toBeVisible();
  await expect(page.getByText(first.display, { exact: true })).toBeVisible();
  expect(server.posts).toHaveLength(1);
  expect(server.posts[0].body).toEqual({ expected_generation: '2' });

  const secondPage = await context.newPage();
  const secondGuard = await prepareUser(context, secondPage, {
    tokens: [firstSecret, secondSecret],
    locale: 'en',
    theme: 'light',
    width: 390,
    installObserver: false,
  });
  await installCallerKeyServer(secondPage, server);
  await secondPage.goto(`${USER_ORIGIN}/keys`);
  await expect(secondPage.getByText(first.display, { exact: true })).toBeVisible();
  await secondPage.getByRole('button', { name: 'Replace API key', exact: true }).click();
  server.postSecret = secondSecret;
  server.postMetadata = second;
  await secondPage
    .getByRole('alertdialog')
    .getByRole('button', { name: 'Replace API key', exact: true })
    .click();
  await expect(secondPage.getByText(secondSecret, { exact: true })).toBeVisible();
  await expect(secondPage.getByText(second.display, { exact: true })).toBeVisible();
  expect(server.posts).toHaveLength(2);
  expect(server.posts[1].body).toEqual({ expected_generation: '3' });
  await expect(page.getByText(firstSecret, { exact: true })).toBeVisible();

  await page.reload();
  await expect(page.getByText(second.display, { exact: true })).toBeVisible();
  await expect(page.getByText(first.display, { exact: true })).toHaveCount(0);
  await expect(page.getByText(firstSecret, { exact: true })).toHaveCount(0);
  await expect(page.getByText(secondSecret, { exact: true })).toHaveCount(0);
  await assertNoSensitiveBrowserPersistence(page, [firstSecret, secondSecret]);
  secondGuard.assertNone();
  guard.assertNone();
  await secondPage.close();
});

test('clears one-time plaintext across logout and a later login', async ({ context, page }) => {
  const secret = `nbk_${'E'.repeat(42)}Q`;
  const server = successServer('0', null, secret, metadata('1', 'IIII'));
  const guard = await prepareUser(context, page, {
    tokens: [secret],
    locale: 'en',
    theme: 'light',
    width: 1_440,
    mockLogout: true,
  });
  await installCallerKeyServer(page, server);
  await page.goto(`${USER_ORIGIN}/keys`);
  await page.getByRole('button', { name: 'Create API key', exact: true }).click();
  await expect(page.getByText(secret, { exact: true })).toBeVisible();

  await page.locator('.nb-account-trigger').click();
  await page.getByRole('button', { name: 'Sign out', exact: true }).click();
  await expect(page).toHaveURL(`${USER_ORIGIN}/`);
  await expect(page.getByText(secret, { exact: true })).toHaveCount(0);

  // A fresh visit loads the signed-in fixture for the later login directly.
  await page.goto(`${USER_ORIGIN}/keys`);
  await expect(
    page.getByText('Key identifier (cannot be used for calls)', { exact: true }),
  ).toBeVisible();
  await expect(page.getByText(secret, { exact: true })).toHaveCount(0);
  await expect(page.locator('.core-secret-value')).toHaveCount(0);
  await assertNoSensitiveBrowserPersistence(page, [secret]);
  guard.assertNone();
});
