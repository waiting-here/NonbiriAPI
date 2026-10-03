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
  it('keeps a manually entered cache path visible until its value is complete or an explicit TTL is chosen', async () => {
    let sent: Record<string, unknown> | undefined;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_url: string, options?: RequestInit) => {
        if (options?.method === 'PUT')
          sent = JSON.parse(String(options.body)) as Record<string, unknown>;
        return response(
          options?.method === 'PUT'
            ? {
                ...projection(),
                body_defaults: {
                  mode: 'replace',
                  values: { '/cache_control': { has_value: true, mask: '••••' } },
                },
              }
            : projection(),
        );
      }),
    );
    const view = await renderWithProviders(
      <RequestAdaptationEditor
        url="/api/endpoints/11/request-adaptation"
        scope="endpoint"
        connectorType="ai-sdk-gateway-v3"
        editable
      />,
      { station: 'user', role: 'user' },
    );
    const defaults = await screen.findByRole('group', { name: 'Default parameters (when absent)' });
    await view.user.click(within(defaults).getByRole('button', { name: 'Add field' }));
    const path = within(defaults).getByRole('textbox', { name: 'Header or path' });
    await view.user.type(path, '/cache_control');
    expect(path).toHaveValue('/cache_control');
    expect(screen.getByLabelText('Automatic cache default')).toHaveValue('custom');
    await view.user.selectOptions(within(defaults).getByLabelText('Edit'), 'clear');
    expect(screen.getByLabelText('Automatic cache default')).toHaveValue('custom');
    await view.user.selectOptions(within(defaults).getByLabelText('Edit'), 'replace');
    const value = within(defaults).getByRole('textbox', { name: 'Value' });
    await view.user.click(value);
    await view.user.paste('{ "type": "ephemeral", "ttl": "1h" }');
    expect(value).toBeInTheDocument();
    await view.user.click(screen.getByRole('button', { name: 'Save request rewriting' }));
    await screen.findByText('Request rewriting saved.');
    expect(sent?.body_defaults).toEqual({
      mode: 'replace',
      values: { '/cache_control': { action: 'replace', value: { type: 'ephemeral', ttl: '1h' } } },
    });
    await view.user.click(within(defaults).getByRole('button', { name: 'Add field' }));
    await view.user.type(
      within(defaults).getByRole('textbox', { name: 'Header or path' }),
      '/cache_control',
    );
    await view.user.selectOptions(screen.getByLabelText('Automatic cache default'), '5m');
    expect(within(defaults).queryByRole('textbox', { name: 'Value' })).not.toBeInTheDocument();
    await view.user.click(screen.getByRole('button', { name: 'Save request rewriting' }));
    await screen.findByText('Request rewriting saved.');
    expect(sent?.body_defaults).toEqual({
      mode: 'replace',
      values: { '/cache_control': { action: 'replace', value: { type: 'ephemeral', ttl: '5m' } } },
    });
  });
  it.each(['5m', '1h'] as const)(
    'sets the Gateway cache default to %s without JSON editing and clears it explicitly',
    async (ttl) => {
      const bodies: Record<string, unknown>[] = [];
      vi.stubGlobal(
        'fetch',
        vi.fn(async (_url: string, options?: RequestInit) => {
          const current = projection();
          current.body_defaults.values = { '/temperature': { has_value: true, mask: '••••' } };
          if (options?.method === 'PUT') {
            bodies.push(JSON.parse(String(options.body)) as Record<string, unknown>);
            current.body_defaults.values = {
              ...current.body_defaults.values,
              '/cache_control': { has_value: true, mask: '••••' },
            };
          }
          return response(current);
        }),
      );
      const view = await renderWithProviders(
        <RequestAdaptationEditor
          url="/admin/api/charity-models/8/request-adaptation"
          scope="charity-model"
          gatewayCacheDefaults
          editable
        />,
        { station: 'admin', role: 'admin' },
      );
      const cache = await screen.findByLabelText('Automatic cache default');
      expect(cache).toHaveValue('off');
      await view.user.selectOptions(cache, ttl);
      await view.user.click(screen.getByRole('button', { name: 'Save request adaptation' }));
      await screen.findByText('Request adaptation saved.');
      expect(bodies[0].body_defaults).toEqual({
        mode: 'replace',
        values: {
          '/temperature': { action: 'keep' },
          '/cache_control': { action: 'replace', value: { type: 'ephemeral', ttl } },
        },
      });
      expect(screen.getByLabelText('Automatic cache default')).toHaveValue('keep');
      await view.user.click(screen.getByRole('button', { name: 'Save request adaptation' }));
      await waitFor(() => expect(bodies).toHaveLength(2));
      expect(
        (bodies[1].body_defaults as { values: Record<string, unknown> }).values['/cache_control'],
      ).toEqual({ action: 'keep' });
      await waitFor(() =>
        expect(screen.getByRole('button', { name: 'Save request adaptation' })).toBeEnabled(),
      );
      await view.user.selectOptions(screen.getByLabelText('Automatic cache default'), 'off');
      expect(screen.queryByText('Request adaptation saved.')).not.toBeInTheDocument();
      await view.user.click(screen.getByRole('button', { name: 'Save request adaptation' }));
      await waitFor(() => expect(bodies).toHaveLength(3));
      expect(
        (bodies[2].body_defaults as { values: Record<string, unknown> }).values['/cache_control'],
      ).toEqual({ action: 'clear' });
    },
  );

  it('retains a chosen Gateway TTL after failure and preserves inherited binding defaults until overridden', async () => {
    let fail = true;
    const bodies: Record<string, unknown>[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_url: string, options?: RequestInit) => {
        if (options?.method === 'PUT') {
          bodies.push(JSON.parse(String(options.body)) as Record<string, unknown>);
          if (fail)
            return new Response(
              JSON.stringify({ error: { code: 'invalid_request', message: 'Rejected' } }),
              { status: 400, headers: { 'Content-Type': 'application/json' } },
            );
        }
        return response({
          ...projection(),
          body_defaults: { mode: 'inherit', values: {} },
          effective: {
            ...projection('1/1'),
            body_defaults: {
              mode: 'replace',
              values: { '/cache_control': { has_value: true, mask: '••••' } },
              source: 'charity_model',
            },
          },
        });
      }),
    );
    const view = await renderWithProviders(
      <RequestAdaptationEditor
        url="/admin/api/charity-models/8/bindings/31/request-adaptation"
        scope="binding"
        connectorType="ai-sdk-gateway-v3"
        editable
      />,
      { station: 'admin', role: 'admin' },
    );
    await screen.findByText(/The cache default inherits with the body defaults/);
    expect(screen.queryByLabelText('Automatic cache default')).not.toBeInTheDocument();
    await view.user.click(screen.getByRole('button', { name: 'Save request adaptation' }));
    await screen.findByText(/Could not save request adaptation/);
    expect(bodies[0].body_defaults).toEqual({ mode: 'inherit', values: {} });
    const defaults = screen.getByRole('group', { name: 'Body defaults (only when absent)' });
    await view.user.selectOptions(
      within(defaults).getByLabelText('Configuration source'),
      'replace',
    );
    await view.user.selectOptions(screen.getByLabelText('Automatic cache default'), '1h');
    await view.user.click(screen.getByRole('button', { name: 'Save request adaptation' }));
    await screen.findByText(/Could not save request adaptation/);
    expect(screen.getByLabelText('Automatic cache default')).toHaveValue('1h');
    expect(bodies[1].body_defaults).toEqual({
      mode: 'replace',
      values: { '/cache_control': { action: 'replace', value: { type: 'ephemeral', ttl: '1h' } } },
    });
    fail = false;
    await view.user.click(screen.getByRole('button', { name: 'Save request adaptation' }));
    await screen.findByText('Request adaptation saved.');
  });

  it('shows the Gateway cache control read-only in Chinese without exposing the saved value', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        response({
          ...projection(),
          body_defaults: {
            mode: 'replace',
            values: { '/cache_control': { has_value: true, mask: '••••' } },
          },
        }),
      ),
    );
    await renderWithProviders(
      <RequestAdaptationEditor
        url="/api/steward/charity-models/8/bindings/31/request-adaptation"
        scope="binding"
        connectorType="ai-sdk-gateway-v3"
        editable={false}
      />,
      { station: 'user', role: 'level4', locale: 'zh' },
    );
    const control = await screen.findByLabelText('自动缓存默认值');
    expect(control).toBeDisabled();
    expect(control).toHaveValue('keep');
    expect(screen.queryByRole('textbox', { name: '值' })).not.toBeInTheDocument();
  });

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
    const forced = await screen.findByRole('group', { name: 'Fixed parameters (always override)' });
    expect(screen.queryByLabelText('Automatic cache default')).not.toBeInTheDocument();
    await rendered.user.click(within(forced).getByRole('button', { name: 'Add field' }));
    await rendered.user.type(
      within(forced).getByRole('textbox', { name: 'Header or path' }),
      '/max_tokens',
    );
    await rendered.user.type(within(forced).getByRole('textbox', { name: 'Value' }), '333');
    await rendered.user.click(screen.getByRole('button', { name: 'Save request rewriting' }));
    await waitFor(() => expect(sent).toBeDefined());
    expect(sent?.expected_revision).toBe('1');
    expect(
      (sent?.fixed_headers as { values: Record<string, unknown> }).values['X-Research'],
    ).toEqual({ action: 'keep' });
    expect(
      (sent?.body_forced as { values: Record<string, unknown> }).values['/max_tokens'],
    ).toEqual({ action: 'replace', value: 333 });
    expect(screen.getByText('Request rewriting saved.')).toBeInTheDocument();
    const forwarded = screen.getByRole('group', { name: 'Allowed client headers' });
    await rendered.user.type(within(forwarded).getByRole('textbox'), '\nX-Other');
    expect(screen.queryByText('Request rewriting saved.')).not.toBeInTheDocument();
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
      return response(getCount === 1 ? projection() : projection('2', ['X-Authoritative']));
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
      within(screen.getByRole('group', { name: 'Allowed client headers' })).getByRole('textbox');
    await screen.findByRole('group', { name: 'Allowed client headers' });
    const headers = forwardedHeaders();
    await rendered.user.clear(headers);
    await rendered.user.type(headers, 'X-Dirty');
    const fixed = screen.getByRole('group', { name: 'Headers added to requests' });
    await rendered.user.selectOptions(
      within(fixed).getByRole('combobox', { name: 'Edit' }),
      'replace',
    );
    const secret = within(fixed).getByLabelText('Value');
    await rendered.user.type(secret, 'unsaved-secret');
    await rendered.i18n.changeLanguage('zh-CN');

    expect(await screen.findByRole('heading', { name: '请求改写' })).toBeInTheDocument();
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(headers).toHaveValue('X-Dirty');
    expect(secret).toHaveValue('unsaved-secret');
    expect(screen.getByRole('button', { name: '保存请求改写' })).toBeEnabled();
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
      within(screen.getByRole('group', { name: 'Allowed client headers' })).getByRole('textbox');
    await screen.findByRole('group', { name: 'Allowed client headers' });
    const headers = forwardedHeaders();
    await rendered.user.clear(headers);
    await rendered.user.type(headers, 'X-Dirty');
    await rendered.user.click(screen.getByRole('button', { name: 'Refresh configuration' }));

    expect(
      await screen.findByText(
        'The displayed settings could not be refreshed and may be stale. Refresh successfully before saving.',
      ),
    ).toBeInTheDocument();
    const save = screen.getByRole('button', { name: 'Save request rewriting' });
    expect(save).toBeDisabled();

    await rendered.user.click(screen.getByRole('button', { name: 'Refresh configuration' }));
    await waitFor(() => expect(screen.getAllByRole('textbox')[0]).toHaveValue('X-Authoritative'));
    expect(screen.getByRole('button', { name: 'Save request rewriting' })).toBeEnabled();
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
        await screen.findByRole('button', { name: 'Save request rewriting' }),
      );
      expect(
        await screen.findByText('The save outcome is uncertain. Refresh before trying again.'),
      ).toBeInTheDocument();
      const save = screen.getByRole('button', { name: 'Save request rewriting' });
      expect(save).toBeDisabled();

      await rendered.user.click(screen.getByRole('button', { name: 'Refresh configuration' }));
      await waitFor(() =>
        expect(
          within(screen.getByRole('group', { name: 'Allowed client headers' })).getByRole(
            'textbox',
          ),
        ).toHaveValue('X-Authoritative'),
      );
      expect(screen.getByRole('button', { name: 'Save request rewriting' })).toBeEnabled();
    },
  );
});
