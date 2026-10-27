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
import { numberedPage } from './numbered-fixtures';

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
  key_count: index + 1 === 11 ? '1' : '0',
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

async function installEndpointRoutes(
  page: Parameters<typeof mockJson>[0],
  entryRequests: URLSearchParams[],
  bindingRequests: URLSearchParams[],
): Promise<void> {
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
      entryRequests.push(url.searchParams);
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
  await page.route(`${USER_ORIGIN}/api/endpoints/*`, async (route) => {
    const url = new URL(route.request().url());
    const selected = endpoints.find((row) => row.id === url.pathname.split('/').pop());
    if (!selected || !/^\/api\/endpoints\/[0-9]+$/.test(url.pathname)) {
      await route.fallback();
      return;
    }
    await route.fulfill({ json: selected });
  });
  await page.route(`${USER_ORIGIN}/api/endpoints/*/keys?**`, (route) => {
    const url = new URL(route.request().url());
    const endpointId = url.pathname.split('/')[3];
    return route.fulfill({
      json: numberedPage(endpointId === endpoint.id ? [endpointKey] : [], url.searchParams),
    });
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
  await page.route(`${USER_ORIGIN}/api/endpoints/11/keys/2/bindings?**`, (route) => {
    const params = new URL(route.request().url()).searchParams;
    bindingRequests.push(params);
    return route.fulfill({ json: numberedPage(bindings, params) });
  });
}

test('entry detail keeps connection list return state through nested paging, refresh, and POP', async ({
  context,
  page,
}) => {
  const consoleGuard = collectConsoleViolations(page);
  await installURLPersistenceObserver(context, [EPHEMERAL_MARKER]);
  await mockPublicConfig(page, 'user');
  await mockRoleSession(page, 'user', 'user');
  const entryRequests: URLSearchParams[] = [];
  const bindingRequests: URLSearchParams[] = [];
  await installEndpointRoutes(page, entryRequests, bindingRequests);

  const listURL =
    USER_ORIGIN + '/endpoints?connections_q=Return&connections_page=2&connections_page_size=10';
  await page.goto(listURL);
  await expect(
    page.getByRole('heading', { name: 'My services', level: 1, exact: true }),
  ).toBeVisible();
  await page.getByRole('link', { name: 'Return connection 11', exact: true }).click();
  await expect(
    page.getByRole('heading', { name: 'Return connection 11', exact: true }),
  ).toBeVisible();
  const endpointPicker = page.getByRole('combobox', { name: 'Endpoint', exact: true });
  const endpointPagination = endpointPicker.locator(
    'xpath=../following-sibling::nav[contains(@class,"page-pagination")]',
  );
  await endpointPicker.selectOption('2');
  await endpointPicker.selectOption('1');
  await endpointPagination.getByRole('combobox', { name: 'Items per page' }).selectOption('10');
  await expect
    .poll(() => new URL(page.url()).searchParams.get('service-endpoint-switch_page_size'))
    .toBe('10');
  const firstEndpointPageURL = page.url();
  await endpointPagination.getByRole('button', { name: '2', exact: true }).click();
  await expect
    .poll(() => new URL(page.url()).searchParams.get('service-endpoint-switch_page'))
    .toBe('2');
  await expect(endpointPicker.locator('option[value="11"]')).toHaveCount(1);
  const secondEndpointPageURL = page.url();
  await page.goBack();
  await expect(page).toHaveURL(firstEndpointPageURL);
  await expect(endpointPicker).toHaveValue('1');
  await expect(endpointPicker.locator('option[value="11"]')).toHaveCount(0);
  await page.goForward();
  await expect(page).toHaveURL(secondEndpointPageURL);
  await expect(endpointPicker.locator('option[value="11"]')).toHaveCount(1);
  const beforeSelectionURL = page.url();
  await endpointPicker.selectOption('11');
  await expect(page.getByRole('heading', { name: endpoint.note, exact: true })).toBeVisible();
  const entryURL = page.url();
  expect(new URL(entryURL).searchParams.get('endpoint_id')).toBe('11');
  expect(new URL(entryURL).searchParams.get('connections_q')).toBe('Return');
  expect(new URL(entryURL).searchParams.get('service-endpoint-switch_page_size')).toBe('10');

  await page.goBack();
  await expect(page).toHaveURL(beforeSelectionURL);
  await expect(endpointPicker).toHaveValue('1');
  await page.goForward();
  await expect(page).toHaveURL(entryURL);
  await expect(endpointPicker).toHaveValue('11');
  await page.reload();
  await expect(endpointPicker).toHaveValue('11');
  await expect(endpointPagination.getByRole('combobox', { name: 'Items per page' })).toHaveValue(
    '10',
  );
  expect(new URL(page.url()).searchParams.get('service-endpoint-switch_page')).toBe('2');
  await expect(page.getByRole('heading', { name: endpoint.note, exact: true })).toBeVisible();

  await page.getByRole('button', { name: 'Add key', exact: true }).click();
  await page.locator('input[type="password"]').fill(EPHEMERAL_MARKER);
  await assertNoSensitiveBrowserPersistence(page, [EPHEMERAL_MARKER]);

  const showAll = page.getByRole('button', { name: /11 associated model/ });
  await showAll.click();
  await expect.poll(() => new URL(page.url()).searchParams.get('routes_2_page')).toBe('1');
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
  await expect.poll(() => new URL(page.url()).searchParams.get('routes_2_page_size')).toBe('10');
  expect(new URL(page.url()).searchParams.get('routes_2_page')).toBe('1');
  await expect(page.getByText('Vendor/Model-10', { exact: true })).toBeVisible();
  await nestedPagination.getByRole('button', { name: 'Next', exact: true }).click();
  await expect.poll(() => new URL(page.url()).searchParams.get('routes_2_page')).toBe('2');
  await expect(page.getByText('Vendor/Model-11', { exact: true })).toBeVisible();
  expect(new URL(page.url()).searchParams.get('endpoint_id')).toBe('11');
  expect(new URL(page.url()).searchParams.get('service-endpoint-switch_page')).toBe('2');
  expect(new URL(page.url()).searchParams.get('connections_q')).toBe('Return');

  await page.goBack();
  await expect.poll(() => new URL(page.url()).searchParams.get('routes_2_page')).toBe('1');
  await expect(page.getByText('Vendor/Model-10', { exact: true })).toBeVisible();
  await page.goForward();
  await expect.poll(() => new URL(page.url()).searchParams.get('routes_2_page')).toBe('2');
  await expect(page.getByText('Vendor/Model-11', { exact: true })).toBeVisible();
  await page.reload();
  await expect(page.getByRole('heading', { name: endpoint.note, exact: true })).toBeVisible();
  await expect(page.getByText('Vendor/Model-11', { exact: true })).toBeVisible();
  await expect(endpointPicker).toHaveValue('11');
  expect(new URL(page.url()).searchParams.get('service-endpoint-switch_page')).toBe('2');
  expect(new URL(page.url()).searchParams.get('routes_2_page_size')).toBe('10');
  await page
    .locator('.nb-expandable-panel:not([hidden])')
    .locator('.nb-expandable-panel__head')
    .getByRole('button', { name: 'Close', exact: true })
    .click();
  await page.getByRole('link', { name: 'Back to my services', exact: true }).click();
  await expect(page).toHaveURL(listURL);
  await expect(page.getByRole('searchbox')).toHaveValue('Return');
  await expect(page.getByRole('link', { name: 'Return connection 11', exact: true })).toBeVisible();
  expect(
    entryRequests.some((params) => params.get('page') === '2' && params.get('page_size') === '10'),
  ).toBe(true);
  expect(entryRequests.every((params) => !params.has('q') && !params.has('source'))).toBe(true);
  expect(
    bindingRequests.some(
      (params) => params.get('page') === '2' && params.get('page_size') === '10',
    ),
  ).toBe(true);

  await page.goto(`${USER_ORIGIN}/endpoints/11?routes_2_page=1&routes_2_page_size=10`);
  await expect(page.getByRole('heading', { name: 'resource return fixture' })).toBeVisible();
  await page
    .locator('.nb-expandable-panel:not([hidden])')
    .locator('.nb-expandable-panel__head')
    .getByRole('button', { name: 'Close', exact: true })
    .click();
  await page.getByRole('main').getByRole('link', { name: 'My services', exact: true }).click();
  await expect(page).toHaveURL(`${USER_ORIGIN}/endpoints`);
  await assertNoSensitiveBrowserPersistence(page, [EPHEMERAL_MARKER]);
  consoleGuard.assertNone();
});
