import { screen, within } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { RequestAdaptationEditor } from './RequestAdaptationEditor';
afterEach(() => vi.unstubAllGlobals());

it('preserves service inheritance while saving personal exclusions and a model header example', async () => {
  let sent: Record<string, unknown> | undefined;
  const projection = {
    revision: '0',
    forward_headers: { mode: 'inherit', values: [] },
    fixed_headers: { mode: 'inherit', values: {} },
    body_defaults: { mode: 'inherit', values: {} },
    body_forced: { mode: 'inherit', values: {} },
    native_extension_paths: { mode: 'inherit', values: [] },
  };
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url, options?: RequestInit) => {
      if (options?.method === 'PUT')
        sent = JSON.parse(String(options.body)) as Record<string, unknown>;
      return new Response(JSON.stringify(projection), {
        headers: { 'Content-Type': 'application/json' },
      });
    }),
  );
  const view = await renderWithProviders(
    <RequestAdaptationEditor
      url="/api/models/7/request-adaptation"
      scope="personal-model"
      editable
    />,
    { station: 'user', role: 'user' },
  );
  await view.user.type(await screen.findByLabelText('Client parameters to exclude'), 'temperature');
  for (const name of [
    'Client headers to forward',
    'Default parameters (when absent)',
    'Fixed parameters (always override)',
    'Extra parameters to keep',
  ]) {
    expect(screen.getByRole('group', { name })).toBeVisible();
  }
  const headers = screen.getByRole('group', { name: 'Fixed outbound headers' });
  await view.user.selectOptions(within(headers).getByLabelText('Configuration source'), 'replace');
  await view.user.click(within(headers).getByRole('button', { name: 'Try this example' }));
  expect(within(headers).getByText(/Enter a header name such as User-Agent/)).toBeVisible();
  await view.user.click(screen.getByRole('button', { name: 'Save request adaptation' }));
  await screen.findByText('Request adaptation saved.');
  expect(sent?.excluded_request_fields).toEqual(['temperature']);
  expect(sent?.fixed_headers).toEqual({
    mode: 'replace',
    values: { 'x-client-version': { action: 'replace', value: '1' } },
  });
  expect(sent?.body_defaults).toEqual({ mode: 'inherit', values: {} });
  expect(sent?.body_forced).toEqual({ mode: 'inherit', values: {} });
  expect(sent?.native_extension_paths).toEqual({ mode: 'inherit', values: [] });
});
