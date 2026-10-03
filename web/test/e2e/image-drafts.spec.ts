import { expect, test } from './test';
import { ADMIN_ORIGIN } from './ports';
import { collectConsoleViolations, mockPublicConfig, mockRoleSession } from './support';

test('administrator preview and draft guard save prices without generation', async ({ page }) => {
  const errors = collectConsoleViolations(page);
  await mockRoleSession(page, 'admin', 'admin');
  await mockPublicConfig(page, 'admin');
  await page.addInitScript(() => localStorage.setItem('nb.lang', 'en'));
  const requests: string[] = [];
  const writes: string[] = [];
  const modelID = 'imdl_AAAAAAAAAAAAAAAAAAAAAA';
  const model = {
    id: modelID,
    upstream_model_id: 'synthetic-image',
    metadata: {},
    configured: true,
    revision: '2',
    display_name: 'Synthetic image',
    description: '',
    enabled: true,
    price: { paper: '2', brush: '1' },
    parameters: [
      {
        key: 'prompt',
        type: 'string',
        supported: true,
        required: true,
        length_unit: 'utf8_bytes',
        max_length: 65536,
      },
      {
        key: 'n',
        type: 'integer',
        supported: true,
        required: false,
        minimum: 1,
        maximum: 4,
        default: 1,
      },
    ],
    capability_issues: [],
    parameter_capabilities: [
      {
        key: 'prompt',
        source: 'profile',
        support: 'supported',
        overridden: false,
        conflict: false,
      },
      { key: 'n', source: 'manual', support: 'supported', overridden: true, conflict: false },
    ],
    combinations: [],
    mapping: { model_pointer: '', parameters: {}, constants: [] },
    capability_revision: '2',
    capability_readiness: 'ready',
    pricing_revision: '2',
    pricing: { default: { paper: '2', brush: '1' }, fallback: 'default', tiers: [], sizes: [] },
    catalog_type: 'image',
    missing: false,
  };
  const adapter = {
    discovery: { method: 'GET', path: '/v1/models', items_pointer: '/data', id_pointer: '/id' },
    submit: {
      method: 'POST',
      path: '/v1/images/generations',
      mapping: { model_pointer: '/model', parameters: { prompt: '/prompt' }, constants: [] },
    },
    response: {
      images_pointer: '/data',
      base64_pointer: '/b64_json',
      working_states: [],
      success_states: [],
      failure_states: [],
    },
  };
  await page.route('**/admin/api/limited-activities/**', async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    requests.push(request.method() + ' ' + path);
    if (path.endsWith('/upstream'))
      return route.fulfill({
        json: {
          revision: '1',
          configured: true,
          base_url: 'https://images.example.invalid',
          secret_set: true,
          rpm: 30,
          concurrency: 2,
          per_user_limit: 1,
          global_limit: 100,
          queue_timeout_seconds: 1800,
          execution_timeout_seconds: 1800,
          memory_budget_mib: 512,
          image_origins: [],
          adapter,
          control: null,
        },
      });
    if (path.endsWith('/upstream/controls'))
      return route.fulfill({ json: { data: [], next_cursor: null } });
    if (path.endsWith('/models/' + modelID)) {
      if (request.method() === 'PUT') {
        writes.push('model');
        model.price = request.postDataJSON().price;
        model.pricing = request.postDataJSON().pricing;
        model.revision = '3';
        return route.fulfill({ json: { id: modelID, revision: '3' } });
      }
      return route.fulfill({ json: model });
    }
    if (path.endsWith('/models'))
      return route.fulfill({
        json: { data: [model], total: 1, revision: '0', page: 1, page_size: 20 },
      });
    return route.fulfill({
      json: {
        key: 'picture-book',
        name: 'Picture book',
        cover_key: 'picture-book',
        visible: false,
        starts_at: null,
        ends_at: null,
        paused: false,
        revision: '1',
        status: 'unconfigured',
        module_config: {
          paper_price: '1',
          brush_price: '1',
          brush_cap: '100',
          brush_exchanged: '0',
          brush_remaining: '100',
        },
      },
    });
  });
  await page.goto(ADMIN_ORIGIN + '/limited-activities?activity=picture-book');
  await page.getByRole('tab', { name: 'Model catalog', exact: true }).click();
  await expect(page.getByLabel(/JSON/)).toHaveCount(0);
  await expect(page.getByRole('option', { name: /synthetic-image/ })).toBeAttached();
  await page.getByLabel('Choose a model to configure').selectOption(modelID);
  await page.getByLabel('Sketch paper per image').fill('5');
  await page.getByRole('tab', { name: 'Image generation service', exact: true }).click();
  await expect(
    page.getByRole('button', { name: 'Save service settings', exact: true }),
  ).toBeDisabled();
  await page.getByRole('tab', { name: 'Model catalog', exact: true }).click();
  await expect(page.getByLabel('Sketch paper per image')).toHaveValue('5');
  await page.getByText('Try parameters and prices (no charge)', { exact: true }).click();
  await expect(page.getByLabel(/^Prompt/)).toBeVisible();
  await page.locator('a[href="/"]').first().click();
  await expect(page.getByRole('button', { name: 'Save and leave' })).toBeVisible();
  await expect(page).toHaveURL(ADMIN_ORIGIN + '/limited-activities?activity=picture-book');
  await page.getByRole('button', { name: 'Continue editing' }).click();
  await expect(page.getByLabel('Sketch paper per image')).toHaveValue('5');
  errors.assertNone();
  await page.locator('a[href="/"]').first().click();
  await page.getByRole('button', { name: 'Save and leave' }).click();
  await expect(page).toHaveURL(ADMIN_ORIGIN + '/');
  expect(writes).toEqual(['model']);
  expect(requests.some((entry) => /capability-profile|capabilities/.test(entry))).toBe(false);
  expect(requests.filter((entry) => /\/quote|\/tasks|\/models\/check/.test(entry))).toEqual([]);
});
