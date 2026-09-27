import { screen, waitFor, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { RequestAdaptationEditor } from './RequestAdaptationEditor';

function projection(revision = '1', forwardHeaders = ['X-Client']) {
  return {
    revision,
    forward_headers: { mode: 'replace', values: forwardHeaders },
    fixed_headers: { mode: 'replace', values: { 'X-Research': { has_value: true, mask: '••••' } } },
    body_defaults: { mode: 'replace', values: {} },
    body_forced: { mode: 'replace', values: {} },
    native_extension_paths: { mode: 'replace', values: [] },
  };
}

function response(value: unknown): Response {
  return new Response(JSON.stringify(value), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

afterEach(() => vi.unstubAllGlobals());

describe('RequestAdaptationEditor', () => {
  it('sends keep for hidden values and JSON for a forced output limit', async () => {
    let sent: Record<string, unknown> | undefined;
    const fetch = vi.fn(async (_url: string, options?: RequestInit) => {
      if (options?.method === 'PUT') {
        sent = JSON.parse(String(options.body)) as Record<string, unknown>;
        expect(new Headers(options.headers).get('Idempotency-Key')).toMatch(
          /^[A-Za-z0-9_-]{22,128}$/,
        );
        return response(projection('2'));
      }
      return response(projection());
    });
    vi.stubGlobal('fetch', fetch);
    const rendered = await renderWithProviders(
      <RequestAdaptationEditor
        url="/api/endpoints/11/request-adaptation"
        scope="endpoint"
        connectorType="openai-compatible"
        editable
      />,
      { station: 'user', role: 'user' },
    );
    const forced = await screen.findByRole('group', { name: 'Forced body values' });
    await rendered.user.click(within(forced).getByRole('button', { name: 'Add field' }));
    await rendered.user.type(
      within(forced).getByRole('textbox', { name: 'Header or path' }),
      '/max_tokens',
    );
    await rendered.user.type(within(forced).getByRole('textbox', { name: 'Value' }), '333');
    await rendered.user.click(screen.getByRole('button', { name: 'Save request adaptation' }));
    await waitFor(() => expect(sent).toBeDefined());
    expect(sent?.expected_revision).toBe('1');
    expect(
      (sent?.fixed_headers as { values: Record<string, unknown> }).values['X-Research'],
    ).toEqual({ action: 'keep' });
    expect(
      (sent?.body_forced as { values: Record<string, unknown> }).values['/max_tokens'],
    ).toEqual({ action: 'replace', value: 333 });
    expect(screen.getByText('Request adaptation saved.')).toBeInTheDocument();
  });

  it('renders an inherited read-only connection projection without revealing values', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        response({
          ...projection('3'),
          forward_headers: { mode: 'inherit', values: [] },
          fixed_headers: { mode: 'inherit', values: {} },
          body_defaults: { mode: 'inherit', values: {} },
          body_forced: { mode: 'inherit', values: {} },
          native_extension_paths: { mode: 'inherit', values: [] },
          effective: {
            ...projection('2/3'),
            fixed_headers: { ...projection().fixed_headers, source: 'charity_model' },
          },
        }),
      ),
    );
    await renderWithProviders(
      <RequestAdaptationEditor
        url="/api/steward/charity-models/8/bindings/31/request-adaptation"
        scope="binding"
        connectorType="anthropic-compatible"
        editable={false}
      />,
      { station: 'user', role: 'level4' },
    );
    expect(await screen.findByText('This setting is read-only for your role.')).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Save request adaptation' }),
    ).not.toBeInTheDocument();
    expect(screen.queryByText('configured-secret')).not.toBeInTheDocument();
    expect(screen.getAllByText('Inherited from the charity model').length).toBeGreaterThan(0);
  });

  it('keeps unsaved fields when the language changes without reloading the projection', async () => {
    let getCount = 0;
    const fetch = vi.fn(async (_url: string, options?: RequestInit) => {
      if (options?.method === 'PUT') throw new Error('Unexpected save');
      getCount += 1;
      return response(
        getCount === 1 ? projection() : projection('2', ['X-Authoritative']),
      );
    });
    vi.stubGlobal('fetch', fetch);
    const rendered = await renderWithProviders(
      <RequestAdaptationEditor
        url="/api/endpoints/11/request-adaptation"
        scope="endpoint"
        connectorType="openai-compatible"
        editable
      />,
      { station: 'user', role: 'user' },
    );

    const forwardedHeaders = () =>
      within(screen.getByRole('group', { name: 'Client headers to forward' })).getByRole(
        'textbox',
      );
    await screen.findByRole('group', { name: 'Client headers to forward' });
    const headers = forwardedHeaders();
    await rendered.user.clear(headers);
    await rendered.user.type(headers, 'X-Dirty');
    const fixed = screen.getByRole('group', { name: 'Fixed outbound headers' });
    await rendered.user.selectOptions(within(fixed).getByRole('combobox', { name: 'Edit' }), 'replace');
    const secret = within(fixed).getByLabelText('Value');
    await rendered.user.type(secret, 'unsaved-secret');
    await rendered.i18n.changeLanguage('zh-CN');

    expect(await screen.findByRole('heading', { name: '请求头与请求主体' })).toBeInTheDocument();
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(headers).toHaveValue('X-Dirty');
    expect(secret).toHaveValue('unsaved-secret');
    expect(screen.getByRole('button', { name: '保存请求适配' })).toBeEnabled();
  });

  it('marks a failed manual refresh stale and keeps saving blocked until refresh succeeds', async () => {
    let getCount = 0;
    const fetch = vi.fn(async (_url: string, options?: RequestInit) => {
      if (options?.method === 'PUT') return response(projection('2'));
      getCount += 1;
      if (getCount === 2) throw new Error('Offline');
      return response(getCount === 1 ? projection() : projection('3', ['X-Authoritative']));
    });
    vi.stubGlobal('fetch', fetch);
    const rendered = await renderWithProviders(
      <RequestAdaptationEditor
        url="/api/endpoints/11/request-adaptation"
        scope="endpoint"
        connectorType="openai-compatible"
        editable
      />,
      { station: 'user', role: 'user' },
    );

    const forwardedHeaders = () =>
      within(screen.getByRole('group', { name: 'Client headers to forward' })).getByRole(
        'textbox',
      );
    await screen.findByRole('group', { name: 'Client headers to forward' });
    const headers = forwardedHeaders();
    await rendered.user.clear(headers);
    await rendered.user.type(headers, 'X-Dirty');
    await rendered.user.click(screen.getByRole('button', { name: 'Refresh configuration' }));

    expect(
      await screen.findByText(
        'The displayed settings could not be refreshed and may be stale. Refresh successfully before saving.',
      ),
    ).toBeInTheDocument();
    const save = screen.getByRole('button', { name: 'Save request adaptation' });
    expect(save).toBeDisabled();

    await rendered.user.click(screen.getByRole('button', { name: 'Refresh configuration' }));
    await waitFor(() => expect(screen.getAllByRole('textbox')[0]).toHaveValue('X-Authoritative'));
    expect(screen.getByRole('button', { name: 'Save request adaptation' })).toBeEnabled();
  });

  it.each(['invalid_json', 'invalid_dto'] as const)(
    'blocks a retry after a successful save response with %s until authoritative refresh',
    async (failure) => {
      let getCount = 0;
      const fetch = vi.fn(async (_url: string, options?: RequestInit) => {
        if (options?.method === 'PUT') {
          return failure === 'invalid_json'
            ? new Response('{', {
                status: 200,
                headers: { 'Content-Type': 'application/json' },
              })
            : response(projection('invalid-revision'));
        }
        getCount += 1;
        return response(getCount === 1 ? projection() : projection('3', ['X-Authoritative']));
      });
      vi.stubGlobal('fetch', fetch);
      const rendered = await renderWithProviders(
        <RequestAdaptationEditor
          url="/api/endpoints/11/request-adaptation"
          scope="endpoint"
          connectorType="openai-compatible"
          editable
        />,
        { station: 'user', role: 'user' },
      );

      await rendered.user.click(
        await screen.findByRole('button', { name: 'Save request adaptation' }),
      );
      expect(
        await screen.findByText('The save outcome is uncertain. Refresh before trying again.'),
      ).toBeInTheDocument();
      const save = screen.getByRole('button', { name: 'Save request adaptation' });
      expect(save).toBeDisabled();

      await rendered.user.click(screen.getByRole('button', { name: 'Refresh configuration' }));
      await waitFor(() =>
        expect(
          within(screen.getByRole('group', { name: 'Client headers to forward' })).getByRole(
            'textbox',
          ),
        ).toHaveValue('X-Authoritative'),
      );
      expect(screen.getByRole('button', { name: 'Save request adaptation' })).toBeEnabled();
    },
  );
});
