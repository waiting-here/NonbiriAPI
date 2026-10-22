import { screen, waitFor, within } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { RequestAdaptationEditor } from './RequestAdaptationEditor';

type Entry = { has_value: true; value?: unknown; mask?: string; sensitive_paths?: string[] };

function projection(revision = '1') {
  return {
    revision,
    forward_headers: { mode: 'replace', values: [] },
    fixed_headers: { mode: 'replace', values: {} as Record<string, Entry> },
    body_defaults: { mode: 'replace', values: {} as Record<string, Entry> },
    body_forced: { mode: 'replace', values: {} as Record<string, Entry> },
    native_extension_paths: { mode: 'replace', values: [] },
  };
}

function response(value: unknown) {
  return new Response(JSON.stringify(value), { headers: { 'Content-Type': 'application/json' } });
}

function fieldRow(name: string) {
  return within(screen.getByDisplayValue(name).closest('.core-field-grid') as HTMLElement);
}

afterEach(() => vi.unstubAllGlobals());

it('shows readable headers and JSON types, preserves untouched values, and refreshes after saving', async () => {
  const current = projection();
  current.fixed_headers.values = {
    'X-Readable': { has_value: true, value: 'client-version' },
    'X-Empty': { has_value: true, value: '' },
  };
  const values: Record<string, unknown> = {
    '/false': false,
    '/true': true,
    '/zero': 0,
    '/null': null,
    '/empty': '',
    '/array': [false, null, ''],
    '/object': { nested: { enabled: false } },
  };
  current.body_defaults.values = Object.fromEntries(
    Object.entries(values).map(([path, value]) => [path, { has_value: true as const, value }]),
  );
  const sent: Record<string, unknown>[] = [];
  let reads = 0;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, options?: RequestInit) => {
      if (options?.method === 'PUT') {
        sent.push(JSON.parse(String(options.body)) as Record<string, unknown>);
        current.revision = '2';
        return response(projection('2'));
      }
      reads += 1;
      return response(current);
    }),
  );
  const view = await renderWithProviders(
    <RequestAdaptationEditor
      url="/api/endpoints/11/request-adaptation"
      scope="endpoint"
      editable
    />,
    { station: 'user', role: 'user' },
  );
  await screen.findByDisplayValue('X-Readable');
  expect(fieldRow('X-Readable').getByLabelText('Value')).toHaveAttribute('type', 'text');
  expect(fieldRow('X-Readable').getByLabelText('Value')).toHaveValue('client-version');
  expect(fieldRow('X-Empty').getByLabelText('Value')).toHaveValue('');
  for (const [path, value] of Object.entries(values)) {
    expect(fieldRow(path).getByLabelText('Value')).toHaveValue(JSON.stringify(value, null, 2));
  }
  await view.user.click(screen.getByRole('button', { name: 'Save request rewriting' }));
  await waitFor(() => expect(reads).toBe(2));
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save request rewriting' })).toBeEnabled(),
  );
  expect(screen.getByText('Request rewriting saved.')).toBeVisible();
  expect(sent[0].fixed_headers).toEqual({
    mode: 'replace',
    values: { 'X-Readable': { action: 'keep' }, 'X-Empty': { action: 'keep' } },
  });
  expect(sent[0].body_defaults).toEqual({
    mode: 'replace',
    values: Object.fromEntries(Object.keys(values).map((path) => [path, { action: 'keep' }])),
  });
  expect(fieldRow('/null').getByLabelText('Value')).toHaveValue('null');
  await view.user.type(fieldRow('X-Readable').getByLabelText('Value'), '-changed');
  expect(fieldRow('X-Readable').getByLabelText('Edit')).toHaveValue('replace');
  await view.user.selectOptions(fieldRow('X-Readable').getByLabelText('Edit'), 'keep');
  expect(fieldRow('X-Readable').getByLabelText('Value')).toHaveValue('client-version');
  await view.user.selectOptions(fieldRow('X-Readable').getByLabelText('Edit'), 'replace');
  expect(fieldRow('X-Readable').getByLabelText('Value')).toHaveValue('client-version');
  await view.user.type(fieldRow('X-Readable').getByLabelText('Value'), '-new');
  await view.user.click(screen.getByRole('button', { name: 'Save request rewriting' }));
  await waitFor(() => expect(sent).toHaveLength(2));
  expect(sent[1].expected_revision).toBe('2');
  expect(sent[1].fixed_headers).toEqual({
    mode: 'replace',
    values: {
      'X-Readable': { action: 'replace', value: 'client-version-new' },
      'X-Empty': { action: 'keep' },
    },
  });
});

it('retains the saved receipt through repeated refresh failures and recovers without another write', async () => {
  let reads = 0;
  let writes = 0;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, options?: RequestInit) => {
      if (options?.method === 'PUT') {
        writes += 1;
        return response(projection('2'));
      }
      reads += 1;
      if (reads === 2 || reads === 3) throw new Error('Offline');
      const current = projection(reads === 1 ? '1' : '2');
      current.fixed_headers.values = { 'X-Readable': { has_value: true, value: 'saved-value' } };
      return response(current);
    }),
  );
  const view = await renderWithProviders(
    <RequestAdaptationEditor
      url="/api/endpoints/11/request-adaptation"
      scope="endpoint"
      editable
    />,
    { station: 'user', role: 'user' },
  );
  await view.user.click(await screen.findByRole('button', { name: 'Save request rewriting' }));
  expect(
    await screen.findByText('Saved, but the latest version could not be loaded.'),
  ).toBeVisible();
  expect(screen.getByRole('button', { name: 'Save request rewriting' })).toBeDisabled();
  expect(screen.queryByText(/Could not save request adaptation/)).not.toBeInTheDocument();
  await view.user.click(screen.getByRole('button', { name: 'Refresh configuration' }));
  await waitFor(() => expect(reads).toBe(3));
  await screen.findByRole('button', { name: 'Save request rewriting' });
  expect(screen.getByText('Saved, but the latest version could not be loaded.')).toBeVisible();
  expect(writes).toBe(1);
  await view.user.click(screen.getByRole('button', { name: 'Refresh configuration' }));
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save request rewriting' })).toBeEnabled(),
  );
  expect(fieldRow('X-Readable').getByLabelText('Value')).toHaveValue('saved-value');
  expect(
    screen.queryByText('Saved, but the latest version could not be loaded.'),
  ).not.toBeInTheDocument();
  expect(writes).toBe(1);
});

it('keeps stored values while toggling whole and leaf protection, then reads their new visibility', async () => {
  const sent: Record<string, unknown>[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, options?: RequestInit) => {
      if (options?.method === 'PUT') {
        sent.push(JSON.parse(String(options.body)) as Record<string, unknown>);
        return response(projection(String(sent.length + 1)));
      }
      const current = projection(String(sent.length + 1));
      const protectedValue = sent.length !== 1;
      current.fixed_headers.values = {
        'X-Token': protectedValue
          ? { has_value: true, mask: '••••', sensitive_paths: [''] }
          : { has_value: true, value: 'retained-header' },
      };
      current.body_forced.values = {
        '/vendor': protectedValue
          ? { has_value: true, mask: '••••', sensitive_paths: ['/token'] }
          : { has_value: true, value: { token: 'retained-token', flag: false } },
      };
      return response(current);
    }),
  );
  const view = await renderWithProviders(
    <RequestAdaptationEditor
      url="/api/endpoints/11/request-adaptation"
      scope="endpoint"
      editable
    />,
    { station: 'user', role: 'user' },
  );
  await screen.findByDisplayValue('X-Token');
  expect(fieldRow('X-Token').getByText('Saved value is hidden')).toBeVisible();
  expect(fieldRow('/vendor').queryByLabelText('Value')).not.toBeInTheDocument();
  for (const name of ['X-Token', '/vendor'])
    await view.user.selectOptions(screen.getByLabelText(`Response protection: ${name}`), 'off');
  await view.user.click(screen.getByRole('button', { name: 'Save request rewriting' }));
  await screen.findByDisplayValue('retained-header');
  expect(fieldRow('/vendor').getByLabelText('Value')).toHaveValue(
    JSON.stringify({ token: 'retained-token', flag: false }, null, 2),
  );
  expect(sent[0].fixed_headers).toEqual({
    mode: 'replace',
    values: { 'X-Token': { action: 'keep', sensitive_paths: [] } },
  });
  expect(sent[0].body_forced).toEqual({
    mode: 'replace',
    values: { '/vendor': { action: 'keep', sensitive_paths: [] } },
  });
  await view.user.selectOptions(screen.getByLabelText('Response protection: X-Token'), 'whole');
  await view.user.selectOptions(screen.getByLabelText('Response protection: /vendor'), 'leaves');
  await view.user.click(screen.getByRole('button', { name: 'Save request rewriting' }));
  await waitFor(() =>
    expect(fieldRow('X-Token').queryByLabelText('Value')).not.toBeInTheDocument(),
  );
  expect(fieldRow('/vendor').queryByLabelText('Value')).not.toBeInTheDocument();
  expect(sent[1].fixed_headers).toEqual({
    mode: 'replace',
    values: { 'X-Token': { action: 'keep', sensitive_paths: [''] } },
  });
  expect(sent[1].body_forced).toEqual({
    mode: 'replace',
    values: { '/vendor': { action: 'keep', sensitive_paths: ['/token'] } },
  });
});

it('shows inherited readable values to a read-only binding viewer and keeps protected entries hidden', async () => {
  const current = projection('3');
  current.fixed_headers.mode = 'inherit';
  current.body_defaults.mode = 'inherit';
  const effective = projection('2/3');
  effective.fixed_headers.values = {
    'X-Plain': { has_value: true, value: 'model-value' },
    'X-Secret': { has_value: true, mask: '••••', sensitive_paths: [''] },
  };
  effective.body_defaults.values = {
    '/enabled': { has_value: true, value: false },
    '/nullable': { has_value: true, value: null },
  };
  const fetch = vi.fn(async () => response({ ...current, effective }));
  vi.stubGlobal('fetch', fetch);
  await renderWithProviders(
    <RequestAdaptationEditor
      url="/api/steward/charity-models/8/bindings/31/request-adaptation"
      scope="binding"
      editable={false}
    />,
    { station: 'user', role: 'level4' },
  );
  await screen.findByDisplayValue('X-Plain');
  expect(fieldRow('X-Plain').getByLabelText('Value')).toHaveValue('model-value');
  expect(fieldRow('X-Plain').getByLabelText('Value')).toHaveAttribute('readonly');
  expect(fieldRow('/enabled').getByLabelText('Value')).toHaveValue('false');
  expect(fieldRow('/nullable').getByLabelText('Value')).toHaveValue('null');
  expect(fieldRow('X-Secret').getByText('Saved value is hidden')).toBeVisible();
  expect(fieldRow('X-Plain').getByLabelText('Edit')).toBeDisabled();
  expect(screen.queryByRole('button', { name: 'Save request adaptation' })).not.toBeInTheDocument();
  expect(fetch).toHaveBeenCalledTimes(1);
});
