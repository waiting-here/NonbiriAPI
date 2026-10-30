import { screen, waitFor, within } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { RequestAdaptationEditor } from './RequestAdaptationEditor';

afterEach(() => vi.unstubAllGlobals());

it('preserves hidden values while marking, editing nested selectors, and explicitly clearing protection', async () => {
  const sent: Record<string, unknown>[] = [];
  const current = {
    revision: '1',
    forward_headers: { mode: 'replace', values: [] },
    fixed_headers: {
      mode: 'replace',
      values: { 'X-Token': { has_value: true, mask: '••••', sensitive_paths: [''] } },
    },
    body_defaults: { mode: 'replace', values: {} },
    body_forced: {
      mode: 'replace',
      values: { '/vendor': { has_value: true, mask: '••••', sensitive_paths: ['/token'] } },
    },
    native_extension_paths: { mode: 'replace', values: [] },
  };
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_url: string, options?: RequestInit) => {
      if (options?.method === 'PUT')
        sent.push(JSON.parse(String(options.body)) as Record<string, unknown>);
      return new Response(JSON.stringify(current), {
        headers: { 'Content-Type': 'application/json' },
      });
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
  const header = await screen.findByRole('combobox', { name: 'Response protection: X-Token' });
  expect(header).toHaveValue('whole');
  expect(screen.getByLabelText('Protected string paths')).toHaveValue('/token');
  await view.user.click(screen.getByRole('button', { name: 'Save request rewriting' }));
  await waitFor(() => expect(sent).toHaveLength(1));
  expect(sent[0].fixed_headers).toEqual({
    mode: 'replace',
    values: { 'X-Token': { action: 'keep' } },
  });
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save request rewriting' })).toBeEnabled(),
  );
  await view.user.selectOptions(header, 'off');
  const paths = screen.getByLabelText('Protected string paths');
  await view.user.clear(paths);
  await view.user.type(paths, '/credentials/0/key\n/token \n/constructor\n/');
  await view.user.click(screen.getByRole('button', { name: 'Save request rewriting' }));
  await waitFor(() => expect(sent).toHaveLength(2));
  expect(sent[1].fixed_headers).toEqual({
    mode: 'replace',
    values: { 'X-Token': { action: 'keep', sensitive_paths: [] } },
  });
  expect(sent[1].body_forced).toEqual({
    mode: 'replace',
    values: {
      '/vendor': {
        action: 'keep',
        sensitive_paths: ['/credentials/0/key', '/token ', '/constructor', '/'],
      },
    },
  });
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save request rewriting' })).toBeEnabled(),
  );
  const field = header.closest('.nb-fields') as HTMLElement;
  await view.user.selectOptions(within(field).getByLabelText('Edit'), 'clear');
  expect(header).toBeDisabled();
  await view.user.click(screen.getByRole('button', { name: 'Save request rewriting' }));
  await waitFor(() => expect(sent).toHaveLength(3));
  expect(sent[2].fixed_headers).toEqual({
    mode: 'replace',
    values: { 'X-Token': { action: 'clear' } },
  });
});
