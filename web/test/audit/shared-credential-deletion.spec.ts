import { randomUUID } from 'node:crypto';
import { readFileSync } from 'node:fs';
import {
  expect,
  test,
  type Browser,
  type BrowserContext,
  type Locator,
  type Page,
} from '@playwright/test';
import common from '../../src/shared/i18n/common/en.json' with { type: 'json' };
import userEn from '../../src/user/i18n/en.json' with { type: 'json' };
import type { NativeService } from '../../src/shared/nativeServices';

const c = common.common.connections;
const core = userEn.user.core;
const services = userEn.user.services;
const charity = userEn.user.charity;
type Identity = { id: string; revision: string };
type Credential = Identity & { credential_version: string; enabled: boolean };
type Entry = Identity & { base_url: string };
type Key = Identity & {
  credential_id: string;
  credential_version: string;
  grant_revision: string;
  credential_available: boolean;
  enabled: boolean;
  native_services: NativeService[];
};
type Source = Identity & {
  discovery_source_revision: string;
  credential_available: boolean;
};
type KeyPage = { data: Key[]; pagination: { total_items: string } };
type Fixture = {
  user_url: string;
  users: { id: string; cookie: { Name: string; Value: string } }[];
  management: { endpoint_id: string };
};

test.skip(
  process.env.NONBIRI_MANAGEMENT_BROWSER !== '1',
  'Requires the isolated management fixture and its real backend.',
);

function fixture(): Fixture {
  return JSON.parse(readFileSync(process.env.NONBIRI_AUDIT_BROWSER_STATE!, 'utf8')) as Fixture;
}
async function session(browser: Browser, state: Fixture) {
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    reducedMotion: 'reduce',
  });
  const cookie = state.users[0]!.cookie;
  await context.addCookies([
    {
      name: cookie.Name,
      value: cookie.Value,
      domain: new URL(state.user_url).hostname,
      path: '/api',
      httpOnly: true,
      sameSite: 'Lax',
    },
  ]);
  await context.addInitScript(() => {
    localStorage.setItem('nb.lang', 'en');
    localStorage.setItem('nb.theme', 'light');
  });
  return context;
}
async function api<T>(
  context: BrowserContext,
  state: Fixture,
  path: string,
  method = 'GET',
  data?: unknown,
  status = method === 'POST' ? 201 : 200,
): Promise<T> {
  const response = await context.request.fetch(state.user_url + path, {
    method,
    headers: {
      Origin: state.user_url,
      ...(method === 'GET' ? {} : { 'Idempotency-Key': randomUUID() }),
    },
    ...(data === undefined ? {} : { data }),
  });
  expect(response.status(), method + ' ' + path).toBe(status);
  return status === 204 ? (undefined as T) : ((await response.json()) as T);
}
function responseFor(page: Page, method: string, path: string) {
  return page.waitForResponse(
    (response) =>
      response.request().method() === method && new URL(response.url()).pathname === path,
  );
}
async function openFold(page: Page, name: string) {
  const fold = page
    .locator('details')
    .filter({ has: page.getByText(name, { exact: true }) })
    .last();
  if ((await fold.getAttribute('open')) === null) await fold.locator(':scope > summary').click();
  return fold;
}
async function navigate(page: Page, path: '/endpoints' | '/charity') {
  // Click the application's Link: goto/reload would discard the query cache under test.
  await page.locator(`a[href="${path}"]:visible`).first().click();
  await expect(page).toHaveURL((url) => url.pathname === path);
}
async function openService(page: Page, name: string) {
  await navigate(page, '/endpoints');
  await page.getByPlaceholder(c.connectionSearch).fill(name);
  await page.getByRole('button', { name: c.search, exact: true }).click();
  await page.getByRole('link', { name, exact: true }).click();
  await expect(page.getByRole('heading', { name, level: 1, exact: true })).toBeVisible();
}
async function openDonationKeys(page: Page, endpoint: Entry, name: string) {
  await navigate(page, '/charity');
  await page.getByRole('tab', { name: charity.tabs.donate, exact: true }).click();
  await page.getByRole('button', { name: charity.presentation.openPicker, exact: true }).click();
  const picker = page.locator('.donation-resource-picker');
  await picker.getByRole('searchbox', { name: charity.resourcePicker.endpointSearch }).fill(name);
  await picker
    .getByRole('searchbox', { name: charity.resourcePicker.endpointSearch })
    .press('Enter');
  const refreshed = page.waitForResponse((response) => {
    const url = new URL(response.url());
    return (
      response.request().method() === 'GET' &&
      url.pathname === `/api/endpoints/${endpoint.id}/keys` &&
      url.searchParams.get('credential_available') === 'true'
    );
  });
  await picker
    .locator('.donation-resource-picker__endpoint-list button')
    .filter({ has: page.getByText(name, { exact: true }) })
    .click();
  const response = await refreshed;
  expect(response.status()).toBe(200);
  await expect(picker).toHaveAttribute('aria-busy', 'false');
  return { picker, candidates: (await response.json()) as KeyPage };
}
function keyChoice(picker: Locator, note: string) {
  return picker
    .locator('.donation-resource-picker__key-list > li')
    .filter({ has: picker.page().getByText(note, { exact: true }) })
    .getByRole('checkbox');
}

test('protocol unlink preserves a shared credential and deletion refreshes donation candidates', async ({
  browser,
}, info) => {
  test.setTimeout(120_000);
  const state = fixture();
  const owner = await session(browser, state);
  const tag = 'Shared deletion ' + randomUUID().slice(0, 8);
  const sharedName = tag + ' shared';
  const openaiName = tag + ' OpenAI';
  const anthropicName = tag + ' Anthropic';
  const remainingName = tag + ' independent';
  let group: Identity | undefined;
  let unexpectedDonation: string | undefined;
  try {
    const base = (await api<Entry>(owner, state, '/api/endpoints/' + state.management.endpoint_id))
      .base_url;
    group = await api<Identity>(owner, state, '/api/connections', 'POST', { name: tag });
    const createCredential = (note: string, secret: string) =>
      api<Credential>(owner, state, `/api/connections/${group!.id}/credentials`, 'POST', {
        note,
        secret,
        ownership_confirmed: true,
      });
    const shared = await createCredential(sharedName, 'synthetic-image-credential');
    const independent = await createCredential(
      remainingName,
      'synthetic-independent-' + randomUUID(),
    );
    const source = await api<Source>(
      owner,
      state,
      `/api/connections/${group.id}/discovery-sources`,
      'POST',
      {
        name: tag + ' catalogue',
        base_url: base,
        list_format: 'openai',
        credential_id: shared.id,
        enabled: true,
      },
    );
    const createEntry = (note: string, connector_type: string, native_services: NativeService[]) =>
      api<Entry>(owner, state, '/api/endpoints', 'POST', {
        connection_id: group!.id,
        source: 'custom',
        connector_type,
        base_url: base,
        note,
        enabled: true,
        native_services,
      });
    const openai = await createEntry(openaiName, 'openai-compatible', ['chat_completions']);
    const anthropic = await createEntry(anthropicName, 'anthropic-compatible', [
      'anthropic_messages',
    ]);
    const bind = (
      entry: Entry,
      credential: Credential,
      note: string,
      native_services: NativeService[],
    ) =>
      api<Key>(owner, state, `/api/endpoints/${entry.id}/keys`, 'POST', {
        credential_id: credential.id,
        note,
        enabled: true,
        ownership_confirmed: true,
        force_store_false: false,
        native_services,
        // A discovery source belongs to one service key; the credential is shared.
        discovery_source_id:
          credential.id === shared.id && entry.id === anthropic.id ? source.id : null,
      });
    const removed = await bind(openai, shared, openaiName, ['chat_completions']);
    const kept = await bind(anthropic, shared, anthropicName, ['anthropic_messages']);
    const valid = await bind(anthropic, independent, remainingName, ['anthropic_messages']);
    expect(removed.credential_id).toBe(kept.credential_id);
    expect(removed.credential_version).toBe(kept.credential_version);
    const page = await owner.newPage();
    const pageErrors: string[] = [];
    page.on('pageerror', (error) => pageErrors.push(error.message));
    await page.goto(
      `${state.user_url}/endpoints?connection_id=${group.id}&endpoint_id=${openai.id}`,
    );
    const navigationMarker = randomUUID();
    await page.evaluate((marker) => {
      (window as Window & { sharedDeletionMarker?: string }).sharedDeletionMarker = marker;
    }, navigationMarker);
    const removedRow = page.locator('tr.core-key-card').filter({ hasText: sharedName });
    await removedRow
      .getByRole('button', { name: `${services.more} · ${sharedName}`, exact: true })
      .click();
    await page
      .getByRole('menuitem', { name: core['endpoints.deleteKey'] + '…', exact: true })
      .click();
    const unlinkDialog = page.getByRole('alertdialog', {
      name: core['endpoints.deleteKeyTitle'],
      exact: true,
    });
    await expect(unlinkDialog).toContainText(core['endpoints.deleteKeyBody']);
    const unlinked = responseFor(page, 'DELETE', `/api/endpoints/${openai.id}/keys/${removed.id}`);
    await unlinkDialog
      .getByRole('button', { name: core['endpoints.deleteKey'], exact: true })
      .click();
    expect((await unlinked).status()).toBe(204);
    await expect(removedRow).toHaveCount(0);
    expect(await api<Credential>(owner, state, '/api/credentials/' + shared.id)).toMatchObject({
      revision: shared.revision,
      credential_version: shared.credential_version,
      enabled: true,
    });
    const keptPath = `/api/endpoints/${anthropic.id}/keys/${kept.id}`;
    const currentKeys = await api<KeyPage>(
      owner,
      state,
      `/api/endpoints/${anthropic.id}/keys?page=1&page_size=20`,
    );
    const snapshot = currentKeys.data.find((key) => key.id === kept.id)!;
    expect(snapshot).toMatchObject({ credential_available: true, enabled: true });
    await page.getByRole('combobox', { name: c.entry, exact: true }).selectOption(anthropic.id);
    await page
      .getByRole('combobox', { name: services.currentKey, exact: true })
      .selectOption(kept.id);
    const keptRow = page.locator('tr.core-key-card').filter({ hasText: sharedName });
    const discovery = responseFor(page, 'POST', keptPath + '/models/refresh');
    await keptRow
      .getByRole('button', { name: core['endpoints.refreshDiscovery'], exact: true })
      .click();
    expect((await discovery).status()).toBe(202);
    await expect
      .poll(
        async () => {
          const result = await api<{ evidence: { state: string } }>(
            owner,
            state,
            keptPath + '/models',
          );
          return result.evidence.state;
        },
        { timeout: 25_000 },
      )
      .toBe('succeeded');
    const models = await api<{ automatic_entries: { upstream_model_id: string }[] }>(
      owner,
      state,
      keptPath + '/models',
    );
    expect(models.automatic_entries.length).toBeGreaterThan(0);
    const liveSource = await api<Source>(owner, state, '/api/discovery-sources/' + source.id);
    expect(liveSource.credential_available).toBe(true);

    const primedAt = Date.now();
    const before = await openDonationKeys(page, anthropic, anthropicName);
    expect(before.candidates.pagination.total_items).toBe('2');
    await keyChoice(before.picker, anthropicName).check();
    await keyChoice(before.picker, remainingName).check();
    const staleSelection = {
      endpoint_key_id: snapshot.id,
      credential_id: snapshot.credential_id,
      credential_version: snapshot.credential_version,
      grant_revision: snapshot.grant_revision,
      native_services: snapshot.native_services,
      expires_at: null,
      discovery_source_id: liveSource.id,
      discovery_source_revision: liveSource.discovery_source_revision,
    };
    await page
      .getByRole('button', { name: charity.presentation.selectionDone, exact: true })
      .click();
    await openService(page, tag);
    const credentials = await openFold(page, services.manageServiceKeys);
    await credentials
      .getByRole('table', { name: c.credentials, exact: true })
      .getByRole('row')
      .filter({ hasText: sharedName })
      .getByRole('button', { name: c.manage, exact: true })
      .click();
    await credentials.getByRole('button', { name: c.deleteCredential, exact: true }).click();
    const deleteDialog = page.getByRole('alertdialog', { name: c.deleteCredential, exact: true });
    await expect(deleteDialog).toContainText(c.allCredentialUsesEffect);
    const deleted = responseFor(page, 'DELETE', '/api/credentials/' + shared.id);
    await deleteDialog.getByRole('button', { name: c.delete, exact: true }).click();
    expect((await deleted).status()).toBe(204);
    await expect(deleteDialog).not.toBeVisible();

    const after = await openDonationKeys(page, anthropic, anthropicName);
    // The cached candidate page was still fresh: a passing result must not depend on its TTL expiring.
    expect(
      Date.now() - primedAt,
      'complete cache invalidation flow inside the 30-second TTL',
    ).toBeLessThan(30_000);
    expect(after.candidates.pagination.total_items).toBe('1');
    expect(after.candidates.data.map((key) => key.id)).toEqual([valid.id]);
    await expect(keyChoice(after.picker, anthropicName)).toHaveCount(0);
    await keyChoice(after.picker, remainingName).check();
    await page
      .getByRole('button', { name: charity.presentation.selectionDone, exact: true })
      .click();
    await expect(page.locator('.donation-selected-chips')).toContainText(remainingName);
    await page.getByRole('button', { name: charity.presentation.openPicker, exact: true }).click();
    await expect(keyChoice(page.locator('.donation-resource-picker'), remainingName)).toBeChecked();
    await expect(keyChoice(page.locator('.donation-resource-picker'), anthropicName)).toHaveCount(
      0,
    );
    expect(
      await page.evaluate(
        () => (window as Window & { sharedDeletionMarker?: string }).sharedDeletionMarker,
      ),
    ).toBe(navigationMarker);

    const metadata = await api<KeyPage>(
      owner,
      state,
      `/api/endpoints/${anthropic.id}/keys?page=1&page_size=20`,
    );
    expect(metadata.data.find((key) => key.id === kept.id)).toMatchObject({
      credential_available: false,
    });
    expect(metadata.data.find((key) => key.id === valid.id)).toMatchObject({
      credential_available: true,
      enabled: true,
    });
    const rejected = await owner.request.post(state.user_url + '/api/donations', {
      headers: { Origin: state.user_url, 'Idempotency-Key': randomUUID() },
      data: {
        description: tag + ' stale selected credential',
        ownership_authorized: true,
        discord_public_thanks: false,
        keys: [staleSelection],
      },
    });
    if (rejected.status() === 201) unexpectedDonation = ((await rejected.json()) as Identity).id;
    expect(
      rejected.status(),
      'deleted credential must be rejected without an internal server error',
    ).toBeGreaterThanOrEqual(400);
    expect(rejected.status()).toBeLessThan(500);
    expect(pageErrors).toEqual([]);
    await info.attach('shared-credential-deletion', {
      body: JSON.stringify(
        {
          protocol_unlink_status: 204,
          remaining_discovery: 'succeeded',
          credential_delete_status: 204,
          candidates_before: before.candidates.pagination.total_items,
          candidates_after: after.candidates.pagination.total_items,
          stale_submission_status: rejected.status(),
          same_document_navigation: true,
        },
        null,
        2,
      ),
      contentType: 'application/json',
    });
  } finally {
    try {
      if (unexpectedDonation) {
        const current = await api<Identity & { status: string }>(
          owner,
          state,
          '/api/donations/' + unexpectedDonation,
        );
        if (current.status === 'pending' || current.status === 'approved') {
          await api(
            owner,
            state,
            '/api/donations/' +
              current.id +
              (current.status === 'pending' ? '/withdraw' : '/terminate'),
            'POST',
            {
              expected_revision: current.revision,
              ...(current.status === 'approved' ? { confirmation: 'End synthetic donation' } : {}),
            },
            200,
          );
        }
      }
      if (group) {
        const current = await api<Identity>(owner, state, '/api/connections/' + group.id);
        await api(
          owner,
          state,
          '/api/connections/' + group.id,
          'DELETE',
          { expected_revision: current.revision },
          204,
        );
      }
    } finally {
      await owner.close();
    }
  }
});
