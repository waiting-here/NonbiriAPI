import {
  assertNoSensitiveBrowserPersistence,
  collectConsoleViolations,
  installURLPersistenceObserver,
  mockJson,
  mockPublicConfig,
  mockRoleSession,
} from './support';
import { USER_ORIGIN } from './ports';
import { expect, test } from './test';
import { numberedPage, numberedResponse } from './numbered-fixtures';

const EPHEMERAL_MARKER = 'resource-return-browser-marker';

const endpoint = {
  id: '11',
  connection_id: '11',
  configuration_type: 'openai-compatible',
  native_services: ['chat_completions'],
  rerank_format: '',
  connector_type: 'openai-compatible',
  base_url: 'https://endpoint.example.test/v1',
  origin: { kind: 'custom' },
  note: 'resource return fixture',
  enabled: true,
  revision: '3',
  key_count: '1',
  created_at: 1_700_000_000,
  updated_at: 1_700_000_010,
};

const endpoints = Array.from({ length: 21 }, (_, index) => ({
  ...endpoint,
  id: String(index + 1),
  note: index + 1 === 11 ? endpoint.note : 'resource return fixture ' + (index + 1),
}));
const connections = Array.from({ length: 21 }, (_, index) => ({
  id: String(index + 1),
  name: 'Return connection ' + (index + 1),
  note: '',
  revision: '1',
  entry_count: index === 10 ? String(endpoints.length) : '0',
  credential_count: index === 10 ? '1' : '0',
  created_at: 1_700_000_000,
  updated_at: 1_700_000_010,
}));

function binding(index: number) {
  return {
    id: String(100 + index),
    model_id: String(200 + index),
    model_full_name: `Vendor/Model-${index}`,
    endpoint_id: endpoint.id,
    endpoint_key_id: '2',
    endpoint_base_url: endpoint.base_url,
    connector_type: endpoint.connector_type,
    endpoint_note: endpoint.note,
    display_head: 'sk-a',
    display_tail: 'tail',
    key_note: 'resource return key',
    upstream_model_id: `Vendor/Model-${index}`,
    ord: index,
    max_concurrency: 0,
    max_rpm: 0,
    state: 'available',
  };
}

const bindings = Array.from({ length: 11 }, (_, index) => binding(index + 1));
const endpointKey = {
  id: '2',
  endpoint_id: endpoint.id,
  display_head: 'sk-a',
  display_tail: 'tail',
  note: 'resource return key',
  enabled: true,
  force_store_false: false,
  max_concurrency: 0,
  max_rpm: 0,
  suspension_state: 'none',
  revision: '4',
  created_at: 1_700_000_001,
  updated_at: 1_700_000_011,
  browse: {
    donation_eligibility: 'eligible',
    model_count: '11',
    binding_count: '11',
    available_binding_count: '11',
    discovery: {
      state: 'succeeded',
      revision: '1',
      result: 'nonempty',
      safe_class: 'none',
      observed_at: 1_700_000_012,
      count: '11',
    },
    preview: bindings.slice(0, 3),
  },
};

async function installEndpointRoutes(page: Parameters<typeof mockJson>[0]): Promise<void> {
  await page.route(USER_ORIGIN + '/api/connections**', async (route) => {
    const url = new URL(route.request().url());
    const q = (url.searchParams.get('q') ?? '').toLowerCase();
    let body: unknown;
    if (url.pathname === '/api/connections') {
      body = numberedPage(
        connections.filter((row) => (row.name + ' ' + row.note).toLowerCase().includes(q)),
        url.searchParams,
      );
    } else if (url.pathname === '/api/connections/11') {
      body = connections[10];
    } else if (url.pathname === '/api/connections/11/entries') {
      body = numberedPage(
        endpoints.filter((row) => (row.note + ' ' + row.base_url).toLowerCase().includes(q)),
        url.searchParams,
      );
    } else {
      await route.fallback();
      return;
    }
    await route.fulfill({ json: body });
  });
  await page.route(`${USER_ORIGIN}/api/endpoints/11/keys/2/models?**`, (route) =>
    route.fulfill({
      json: {
        evidence: {
          state: 'unknown',
          revision: '0',
          result: null,
          safe_class: 'none',
          observed_at: null,
          count: null,
        },
        automatic_entries: [],
        manual_entries: [],
        next_cursor: null,
        pagination: { page: '1', page_size: 20, total_items: '0', total_pages: '1' },
      },
    }),
  );

  await mockJson(page, {
    origin: USER_ORIGIN,
    method: 'GET',
    path: '/api/endpoint-create-options',
    body: {
      base_connector_types: ['openai-compatible', 'anthropic-compatible'],
      mainstream_channels: [],
    },
  });
  await mockJson(page, {
    origin: USER_ORIGIN,
    method: 'GET',
    path: '/api/endpoints/11',
    body: endpoint,
  });
  await mockJson(page, {
    origin: USER_ORIGIN,
    method: 'GET',
    path: '/api/endpoints/11/keys?page=1&page_size=20',
    body: numberedResponse([endpointKey], '1', 20, 1, 1),
  });
  for (const pageSize of [10, 20]) {
    for (const pageNumber of [1, 2]) {
      const offset = (pageNumber - 1) * pageSize;
      await mockJson(page, {
        origin: USER_ORIGIN,
        method: 'GET',
        path: `/api/endpoints/11/keys/2/bindings?page=${pageNumber}&page_size=${pageSize}`,
        body: numberedResponse(
          bindings.slice(offset, offset + pageSize),
          String(pageNumber),
          pageSize,
          11,
          Math.max(1, Math.ceil(11 / pageSize)),
        ),
      });
    }
  }
}

test('entry detail keeps connection list return state through nested paging, refresh, and POP', async ({
  context,
  page,
}) => {
  const consoleGuard = collectConsoleViolations(page);
  await installURLPersistenceObserver(context, [EPHEMERAL_MARKER]);
  await mockPublicConfig(page, 'user');
  await mockRoleSession(page, 'user', 'user');
  await installEndpointRoutes(page);

  await page.goto(
    USER_ORIGIN + '/endpoints?connections_q=Return&connections_page=2&connections_page_size=10',
  );
  await expect(
    page.getByRole('heading', { name: 'Service connections', exact: true }),
  ).toBeVisible();
  await page.getByRole('link', { name: 'Return connection 11', exact: true }).click();
  await expect(
    page.getByRole('heading', { name: 'Return connection 11', exact: true }),
  ).toBeVisible();
  const entryList = page.locator('section.nb-panel').filter({
    has: page.getByRole('heading', { name: 'Entries', exact: true }),
  });
  await entryList.getByRole('searchbox').fill('resource');
  await entryList.getByRole('button', { name: 'Search', exact: true }).click();
  await entryList.getByRole('combobox', { name: 'Items per page' }).selectOption('10');
  await entryList.getByRole('button', { name: '2', exact: true }).click();
  await expect(page.getByText(endpoint.note, { exact: true })).toBeVisible();
  await expect
    .poll(() => new URL(page.url()).searchParams.get('connection-entries_page'))
    .toBe('2');
  const entryReturnURL = page.url();
  expect(new URL(entryReturnURL).searchParams.get('connection-entries_q')).toBe('resource');
  expect(new URL(entryReturnURL).searchParams.get('connections_q')).toBe('Return');
  await page.locator('a[href="/endpoints/11"]').click();
  await expect(page.getByRole('heading', { name: 'resource return fixture' })).toBeVisible();
  await expect(page).toHaveURL(USER_ORIGIN + '/endpoints/11');

  const showAll = page.getByRole('button', { name: /11 associated model/ });
  await showAll.click();
  await expect(page).toHaveURL(/\/endpoints\/11\?routes_2_page=1/);
  await expect(page.getByText('Vendor/Model-1', { exact: true })).toBeVisible();

  const routingCard = page
    .locator('section.core-card')
    .filter({
      has: page.getByRole('heading', { name: 'Source status', exact: true }),
    })
    .last();
  const nestedPagination = routingCard.locator('.page-pagination');
  const nestedSize = nestedPagination.getByRole('combobox', { name: 'Items per page' });
  await expect(nestedSize).toHaveValue('20');
  await nestedSize.selectOption('10');
  await expect(page).toHaveURL(/routes_2_page=1&routes_2_page_size=10/);
  await expect(page.getByText('Vendor/Model-10', { exact: true })).toBeVisible();
  await nestedPagination.getByRole('button', { name: 'Next', exact: true }).click();
  await expect(page).toHaveURL(/routes_2_page=2&routes_2_page_size=10/);
  await expect(page.getByText('Vendor/Model-11', { exact: true })).toBeVisible();

  await page.goBack();
  await expect(page).toHaveURL(/routes_2_page=1&routes_2_page_size=10/);
  await page.goForward();
  await expect(page).toHaveURL(/routes_2_page=2&routes_2_page_size=10/);
  await page.reload();
  await expect(page.getByRole('heading', { name: 'resource return fixture' })).toBeVisible();
  await page
    .locator('.nb-expandable-panel:not([hidden])')
    .locator('.nb-expandable-panel__head')
    .getByRole('button', { name: 'Close', exact: true })
    .click();
  await page.getByRole('link', { name: 'My services', exact: true }).click();
  await expect(page).toHaveURL(entryReturnURL);
  await expect(page.getByText(endpoint.note, { exact: true })).toBeVisible();
  await expect(entryList.getByRole('searchbox')).toHaveValue('resource');
  await expect(entryList.getByRole('combobox', { name: 'Items per page' })).toHaveValue('10');
  const connectionReturnURL = new URL(entryReturnURL);
  connectionReturnURL.searchParams.delete('connection_id');
  connectionReturnURL.searchParams.delete('connection_view');
  await page.getByRole('link', { name: 'Back to connections', exact: true }).click();
  await expect(page).toHaveURL(connectionReturnURL.toString());
  await expect(page.getByRole('searchbox')).toHaveValue('Return');
  await expect(page.getByRole('link', { name: 'Return connection 11', exact: true })).toBeVisible();

  await page.goto(`${USER_ORIGIN}/endpoints/11?routes_2_page=1&routes_2_page_size=10`);
  await expect(page.getByRole('heading', { name: 'resource return fixture' })).toBeVisible();
  await page
    .locator('.nb-expandable-panel:not([hidden])')
    .locator('.nb-expandable-panel__head')
    .getByRole('button', { name: 'Close', exact: true })
    .click();
  await page.getByRole('link', { name: 'My services', exact: true }).click();
  await expect(page).toHaveURL(`${USER_ORIGIN}/endpoints`);
  await assertNoSensitiveBrowserPersistence(page, [EPHEMERAL_MARKER]);
  consoleGuard.assertNone();
});
