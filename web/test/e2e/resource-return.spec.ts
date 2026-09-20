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
import { numberedResponse } from './numbered-fixtures';

const EPHEMERAL_MARKER = 'resource-return-browser-marker';

const endpoint = {
  id: '11',
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

const endpointPageTwo = Array.from({ length: 10 }, (_, index) => ({
  ...endpoint,
  id: String(11 + index),
  note: index === 0 ? endpoint.note : `resource return fixture ${index + 1}`,
}));

const endpointPageOne = Array.from({ length: 20 }, (_, index) => ({
  ...endpoint,
  id: String(index + 1),
  note: index + 1 === 11 ? endpoint.note : `resource return fixture ${index + 1}`,
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
    path: '/api/endpoints?page=2&page_size=10',
    body: numberedResponse(endpointPageTwo, '2', 10, 21, 3),
  });
  await mockJson(page, {
    origin: USER_ORIGIN,
    method: 'GET',
    path: '/api/endpoints?page=1&page_size=20',
    body: numberedResponse(endpointPageOne, '1', 20, 21, 2),
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

test('endpoint detail keeps list return state through nested paging, refresh, and POP', async ({
  context,
  page,
}) => {
  const consoleGuard = collectConsoleViolations(page);
  await installURLPersistenceObserver(context, [EPHEMERAL_MARKER]);
  await mockPublicConfig(page, 'user');
  await mockRoleSession(page, 'user', 'user');
  await installEndpointRoutes(page);

  await page.goto(
    `${USER_ORIGIN}/endpoints?page=2&page_size=10`,
  );
  await expect(page.getByRole('heading', { name: 'Resources' })).toBeVisible();
  await expect(page.getByText(endpoint.note, { exact: true })).toBeVisible();
  await page.locator('a[href="/endpoints/11"]').click();
  await expect(page.getByRole('heading', { name: 'Endpoint details' })).toBeVisible();
  await expect(page).toHaveURL(`${USER_ORIGIN}/endpoints/11`);

  const showAll = page.getByRole('button', { name: 'Browse all 11 connections', exact: true });
  await showAll.click();
  await expect(page).toHaveURL(/\/endpoints\/11\?routes_2_page=1/);
  await expect(page.getByText('Vendor/Model-1', { exact: true })).toBeVisible();

  const routingCard = page
    .locator('section.core-card')
    .filter({
      has: page.getByRole('heading', { name: 'Model connection status', exact: true }),
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
  await expect(page.getByRole('heading', { name: 'Endpoint details' })).toBeVisible();
  await page.getByRole('link', { name: 'Back', exact: true }).click();
  await expect(page).toHaveURL(`${USER_ORIGIN}/endpoints?page=2&page_size=10`);
  await expect(page.getByText(endpoint.note, { exact: true })).toBeVisible();

  await page.goto(`${USER_ORIGIN}/endpoints/11?routes_2_page=1&routes_2_page_size=10`);
  await expect(page.getByRole('heading', { name: 'Endpoint details' })).toBeVisible();
  await page.getByRole('link', { name: 'Back', exact: true }).click();
  await expect(page).toHaveURL(`${USER_ORIGIN}/endpoints`);
  await assertNoSensitiveBrowserPersistence(page, [EPHEMERAL_MARKER]);
  consoleGuard.assertNone();
});
