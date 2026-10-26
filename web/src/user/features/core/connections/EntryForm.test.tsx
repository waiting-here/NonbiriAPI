import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../../test/unit/support';
import { EntryForm } from './EntryForm';

vi.mock('./CredentialGrantForm', () => ({
  CredentialGrantForm: ({ discoveryPreset }: { discoveryPreset?: { base_url: string } }) => (
    <p data-testid="grant-preset">{discoveryPreset?.base_url ?? 'none'}</p>
  ),
}));

const firstChannel = `mch_${'A'.repeat(22)}`;
const secondChannel = `mch_${'B'.repeat(21)}A`;
const endpointWire = JSON.parse(
  readFileSync(resolve(process.cwd(), '../internal/resources/testdata/endpoint.json'), 'utf8'),
);
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status, headers: { 'content-type': 'application/json' } });
function options(matching = false) {
  return {
    base_connector_types: ['openai-compatible', 'anthropic-compatible'],
    native_service_options: [
      {
        connector_type: 'openai-compatible',
        configuration_type: 'openai-compatible',
        native_services: ['chat_completions', 'responses'],
        default_native_services: ['chat_completions'],
        rerank_formats: [],
      },
      {
        connector_type: 'openai-chat-completions',
        configuration_type: 'openai-compatible',
        native_services: ['chat_completions', 'responses'],
        default_native_services: ['chat_completions'],
        rerank_formats: [],
      },
      {
        connector_type: 'anthropic-compatible',
        configuration_type: 'anthropic-compatible',
        native_services: ['anthropic_messages'],
        default_native_services: ['anthropic_messages'],
        rerank_formats: [],
      },
    ],
    mainstream_channels: [
      {
        id: firstChannel,
        name: 'First channel',
        revision: matching ? '7' : '6',
        connector_type: 'openai-compatible',
        base_url: 'https://other.example/v1',
        entries: [
          {
            id: '1',
            name: 'Other entry',
            connector_type: 'openai-compatible',
            base_url: 'https://other.example/v1',
            native_services: ['responses'],
            rerank_format: '',
            enabled: true,
          },
          {
            id: '2',
            name: 'Matching entry',
            connector_type: 'openai-chat-completions',
            base_url: 'https://example.com/v1',
            native_services: ['chat_completions', 'responses'],
            rerank_format: '',
            enabled: true,
          },
        ],
        discovery_source: { base_url: 'https://example.com/models', list_format: 'openai' },
        model_mappings: [
          {
            model_id: 'chat-small',
            entries: [
              {
                entry_id: '2',
                upstream_model_id: 'provider-small',
                native_services: ['chat_completions'],
              },
            ],
          },
        ],
      },
      {
        id: secondChannel,
        name: 'Second channel',
        revision: '3',
        connector_type: 'anthropic-compatible',
        base_url: 'https://example.com/v1',
        entries: [
          {
            id: '4',
            name: 'Another match',
            connector_type: 'anthropic-compatible',
            base_url: 'https://example.com/v1',
            native_services: ['anthropic_messages'],
            rerank_format: '',
            enabled: true,
          },
        ],
      },
    ],
    ...(matching
      ? {
          matching_mainstream_entries: [
            { channel_id: firstChannel, entry_id: '2' },
            { channel_id: secondChannel, entry_id: '4' },
          ],
        }
      : {}),
  };
}

async function renderForm(fetchMock: typeof fetch, onChanged?: () => void) {
  vi.stubGlobal('fetch', vi.fn(fetchMock));
  const view = await renderWithProviders(
    <EntryForm accountId="1" connectionId="9" onChanged={onChanged} />,
    {
      station: 'user',
      role: 'user',
      locale: 'en',
    },
  );
  view.queryClient.setQueryData(['user', 'session'], {
    user: { id: '1', username: 'reader', effective_level: 1 },
  });
  await screen.findByRole('combobox', { name: 'Protocol' });
  await view.user.selectOptions(
    screen.getByRole('combobox', { name: 'Protocol' }),
    'openai-compatible',
  );
  return view;
}

describe('mainstream entry suggestions', () => {
  it('shows every match and explicitly adopts only the chosen entry with its revision and all protocol services', async () => {
    const writes: { body: Record<string, unknown>; key: string | null }[] = [];
    const view = await renderForm(async (input, init) => {
      const url = new URL(String(input), 'https://app.example');
      if (url.pathname === '/api/endpoint-create-options')
        return json(options(url.searchParams.has('match_base_url')));
      if (url.pathname === '/api/endpoints' && init?.method === 'POST') {
        const body = JSON.parse(String(init.body));
        writes.push({ body, key: new Headers(init.headers).get('Idempotency-Key') });
        return json(
          {
            ...endpointWire,
            native_services: body.native_services,
            origin: { kind: 'mainstream', channel_id: firstChannel, name: 'First channel' },
          },
          201,
        );
      }
      throw Error('Unexpected ' + url.pathname);
    });
    await view.user.type(
      screen.getByRole('textbox', { name: 'Endpoint URL' }),
      'https://example.com/v1',
    );
    const suggestions = await screen.findByRole('region', {
      name: 'This address is available as a preset',
    });
    expect(within(suggestions).getByText('First channel · Matching entry')).toBeVisible();
    expect(within(suggestions).getByText('OpenAI-compatible')).toBeVisible();
    expect(within(suggestions).queryByText('OpenAI Chat Completions')).not.toBeInTheDocument();
    expect(within(suggestions).getByText('Second channel · Another match')).toBeVisible();
    expect(within(suggestions).getByText('https://example.com/models')).toBeVisible();
    expect(within(suggestions).getByText('chat-small → provider-small')).toBeVisible();
    expect(writes).toHaveLength(0);
    await view.user.click(
      within(suggestions).getByRole('button', {
        name: 'Use this preset: First channel · Matching entry',
      }),
    );
    expect(screen.getByRole('radio', { name: 'Mainstream preset' })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'Matching entry' })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'Other entry' })).not.toBeChecked();
    expect(screen.queryByRole('checkbox', { name: 'Chat completions' })).not.toBeInTheDocument();
    await view.user.click(screen.getByRole('button', { name: 'Save selected entries' }));
    await screen.findByText('Entry saved. Choose a credential and authorize it next.', {
      exact: false,
    });
    expect(writes).toHaveLength(1);
    expect(writes[0]?.key).toBeTruthy();
    expect(screen.getByTestId('grant-preset')).toHaveTextContent('https://example.com/models');
    await view.user.selectOptions(
      screen.getByRole('combobox', { name: 'Mainstream preset' }),
      secondChannel,
    );
    expect(screen.getByTestId('grant-preset')).toHaveTextContent('https://example.com/models');
    expect(writes[0]?.body).toEqual({
      source: 'mainstream',
      channel_id: firstChannel,
      channel_entry_id: '2',
      channel_revision: '7',
      note: 'Matching entry',
      enabled: true,
      connection_id: '9',
      native_services: ['chat_completions', 'responses'],
      rerank_format: '',
    });
  });

  it('keeps the custom draft when switching back and lets it use the original custom submit path', async () => {
    const writes: Record<string, unknown>[] = [];
    const view = await renderForm(async (input, init) => {
      const url = new URL(String(input), 'https://app.example');
      if (url.pathname === '/api/endpoint-create-options')
        return json(options(url.searchParams.has('match_base_url')));
      if (url.pathname === '/api/endpoints' && init?.method === 'POST') {
        const body = JSON.parse(String(init.body));
        writes.push(body);
        return json(
          { ...endpointWire, note: body.note, native_services: body.native_services },
          201,
        );
      }
      throw Error('Unexpected ' + url.pathname);
    });
    await view.user.type(
      screen.getByRole('textbox', { name: 'Endpoint URL' }),
      'https://example.com/v1',
    );
    await view.user.type(screen.getByRole('textbox', { name: 'Note Optional' }), 'My custom draft');
    await view.user.click(
      await screen.findByRole('button', {
        name: 'Use this preset: First channel · Matching entry',
      }),
    );
    await view.user.click(screen.getByRole('radio', { name: 'Custom' }));
    expect(screen.getByRole('textbox', { name: 'Endpoint URL' })).toHaveValue(
      'https://example.com/v1',
    );
    expect(screen.getByRole('textbox', { name: 'Note Optional' })).toHaveValue('My custom draft');
    expect(screen.getByRole('combobox', { name: 'Protocol' })).toHaveValue('openai-compatible');
    expect(screen.queryByRole('checkbox', { name: 'Responses' })).not.toBeInTheDocument();
    await view.user.click(screen.getByRole('button', { name: 'Save selected entries' }));
    await waitFor(() => expect(writes).toHaveLength(1));
    expect(writes[0]).toEqual({
      source: 'custom',
      connector_type: 'openai-compatible',
      base_url: 'https://example.com/v1',
      note: 'My custom draft',
      enabled: true,
      connection_id: '9',
      native_services: ['chat_completions', 'responses'],
      rerank_format: '',
    });
  });

  it('adopts a different protocol without requiring another service selection', async () => {
    const view = await renderForm(async (input) =>
      json(
        options(new URL(String(input), 'https://app.example').searchParams.has('match_base_url')),
      ),
    );
    await view.user.type(
      screen.getByRole('textbox', { name: 'Endpoint URL' }),
      'https://example.com/v1',
    );
    await view.user.click(
      await screen.findByRole('button', {
        name: 'Use this preset: Second channel · Another match',
      }),
    );
    expect(screen.queryByRole('checkbox', { name: 'Anthropic Messages' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Save selected entries' })).toBeEnabled();
  });

  it.each(['empty', 'error'] as const)(
    'allows custom creation when suggestions return %s',
    async (outcome) => {
      const writes: unknown[] = [];
      const view = await renderForm(async (input, init) => {
        const url = new URL(String(input), 'https://app.example');
        if (url.pathname === '/api/endpoint-create-options') {
          if (!url.search) return json(options());
          return outcome === 'empty'
            ? json({ ...options(), matching_mainstream_entries: [] })
            : json({ error: { code: 'internal_error', message: 'Unavailable' } }, 500);
        }
        if (init?.method === 'POST') {
          writes.push(JSON.parse(String(init.body)));
          return json(endpointWire, 201);
        }
        throw Error('Unexpected ' + url.pathname);
      });
      fireEvent.change(screen.getByRole('textbox', { name: 'Endpoint URL' }), {
        target: { value: 'https://other.example/v1' },
      });
      await act(() => new Promise((resolve) => setTimeout(resolve, 450)));
      expect(
        screen.queryByRole('region', { name: 'This address is available as a preset' }),
      ).not.toBeInTheDocument();
      expect(screen.queryByRole('alert')).not.toBeInTheDocument();
      await view.user.click(screen.getByRole('button', { name: 'Save selected entries' }));
      await waitFor(() => expect(writes).toHaveLength(1));
    },
  );

  it('debounces address changes and ignores a cancelled late suggestion', async () => {
    let finishFirst!: (response: Response) => void;
    let firstSignal: AbortSignal | null | undefined;
    const matchingReads: string[] = [];
    await renderForm(async (input, init) => {
      const url = new URL(String(input), 'https://app.example');
      const address = url.searchParams.get('match_base_url');
      if (!address) return json(options());
      matchingReads.push(address);
      if (address === 'https://first.example/v1') {
        firstSignal = init?.signal;
        return new Promise<Response>((resolve) => {
          finishFirst = resolve;
        });
      }
      return json({ ...options(), matching_mainstream_entries: [] });
    });
    const input = screen.getByRole('textbox', { name: 'Endpoint URL' });
    fireEvent.change(input, { target: { value: 'https://first.example/v1' } });
    expect(matchingReads).toEqual([]);
    await waitFor(() => expect(matchingReads).toHaveLength(1));
    fireEvent.change(input, { target: { value: 'https://second.example/v1' } });
    expect(firstSignal?.aborted).toBe(true);
    await waitFor(() => expect(matchingReads).toHaveLength(2));
    await act(async () => finishFirst(json(options(true))));
    expect(
      screen.queryByRole('region', { name: 'This address is available as a preset' }),
    ).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Save selected entries' })).toBeEnabled();
  });

  it('keeps ignored URL components out of matching requests and cache keys', async () => {
    const fetchMock = vi.fn<typeof fetch>(async () => json(options()));
    const view = await renderForm(fetchMock);
    for (const value of [
      'https://private:secret@example.com/v1',
      'https://@example.com/v1',
      'https://example.com/v1?private=secret',
      'https://example.com/v1#private',
      'ftp://example.com/v1',
    ]) {
      fireEvent.change(screen.getByRole('textbox', { name: 'Endpoint URL' }), {
        target: { value },
      });
      await act(() => new Promise((resolve) => setTimeout(resolve, 400)));
    }
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const cache = JSON.stringify(
      view.queryClient
        .getQueryCache()
        .getAll()
        .map((query) => query.queryKey),
    );
    expect(cache).not.toContain('private');
    expect(cache).not.toContain('secret');
  });

  it('does not reuse another account matching result', async () => {
    let matchingReads = 0;
    const view = await renderForm(async (input) => {
      const url = new URL(String(input), 'https://app.example');
      if (!url.search) return json(options());
      matchingReads += 1;
      return json(
        matchingReads === 1 ? options(true) : { ...options(), matching_mainstream_entries: [] },
      );
    });
    fireEvent.change(screen.getByRole('textbox', { name: 'Endpoint URL' }), {
      target: { value: 'https://example.com/v1' },
    });
    await screen.findByRole('region', { name: 'This address is available as a preset' });
    view.rerender(<EntryForm accountId="2" connectionId="9" />);
    expect(
      screen.queryByRole('region', { name: 'This address is available as a preset' }),
    ).not.toBeInTheDocument();
    await waitFor(() => expect(matchingReads).toBe(2));
    expect(
      screen.queryByRole('region', { name: 'This address is available as a preset' }),
    ).not.toBeInTheDocument();
  });

  it('refreshes a stale adopted preset through the existing conflict flow', async () => {
    const revisions: unknown[] = [];
    const view = await renderForm(async (input, init) => {
      const url = new URL(String(input), 'https://app.example');
      if (url.pathname === '/api/endpoint-create-options') {
        const value = options(url.searchParams.has('match_base_url'));
        if (revisions.length) value.mainstream_channels[0]!.revision = '8';
        return json(value);
      }
      if (url.pathname === '/api/endpoints' && init?.method === 'POST') {
        revisions.push(JSON.parse(String(init.body)).channel_revision);
        return revisions.length === 1
          ? json({ error: { code: 'conflict', message: 'Preset changed' } }, 409)
          : json(endpointWire, 201);
      }
      throw Error('Unexpected ' + url.pathname);
    });
    fireEvent.change(screen.getByRole('textbox', { name: 'Endpoint URL' }), {
      target: { value: 'https://example.com/v1' },
    });
    await view.user.click(
      await screen.findByRole('button', {
        name: 'Use this preset: First channel · Matching entry',
      }),
    );
    await view.user.click(screen.getByRole('button', { name: 'Save selected entries' }));
    await screen.findByRole('alert');
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Save selected entries' })).toBeEnabled(),
    );
    await view.user.click(screen.getByRole('button', { name: 'Save selected entries' }));
    await waitFor(() => expect(revisions).toEqual(['7', '8']));
  });
});

it('keeps every saved entry associated with its own selection during batched rendering', async () => {
  const supplied = options();
  const channel = supplied.mainstream_channels[0]!;
  channel.entries.push({ ...channel.entries[0]!, id: '3', name: 'Third entry' });
  channel.model_mappings = [];
  const writes: string[] = [];
  let changed = 0;
  let finishBatch!: () => void;
  const batchFinished = new Promise<void>((resolve) => {
    finishBatch = resolve;
  });
  const view = await renderForm(
    async (input, init) => {
      const url = new URL(String(input), 'https://app.example');
      if (url.pathname === '/api/endpoint-create-options') return json(supplied);
      if (url.pathname === '/api/endpoints' && init?.method === 'POST') {
        const body = JSON.parse(String(init.body));
        writes.push(body.channel_entry_id);
        return json(
          {
            ...endpointWire,
            id: '20' + body.channel_entry_id,
            note: body.note,
            native_services: body.native_services,
            origin: { kind: 'mainstream', channel_id: firstChannel, name: channel.name },
          },
          201,
        );
      }
      throw Error('Unexpected ' + url.pathname);
    },
    () => {
      changed += 1;
      if (changed === channel.entries.length) finishBatch();
    },
  );
  await view.user.click(screen.getByRole('radio', { name: 'Mainstream preset' }));
  await view.user.selectOptions(
    screen.getByRole('combobox', { name: 'Mainstream preset' }),
    firstChannel,
  );
  for (const entry of channel.entries) {
    await view.user.click(screen.getByRole('checkbox', { name: entry.name }));
  }
  // Hold React's render until all successful callbacks have queued their updates.
  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name: 'Save selected entries' }));
    await batchFinished;
  });
  expect(writes).toEqual(['1', '2', '3']);
  expect(
    screen.getAllByText('Entry saved. Choose a credential and authorize it next.', {
      exact: false,
    }),
  ).toHaveLength(3);
  for (const entry of channel.entries) {
    expect(screen.getByRole('heading', { name: entry.name })).toBeVisible();
    expect(screen.getByRole('checkbox', { name: entry.name })).toBeDisabled();
  }
  expect(screen.getByRole('button', { name: 'Save selected entries' })).toBeDisabled();
});

it('uses the complete System One protocol without a sub-service choice', async () => {
  const supplied = options();
  supplied.base_connector_types.push('typesafe-systemone');
  supplied.native_service_options.push({
    connector_type: 'typesafe-systemone',
    configuration_type: 'typesafe-systemone',
    native_services: ['systemone'],
    default_native_services: ['systemone'],
    rerank_formats: [],
  });
  const writes: Record<string, unknown>[] = [];
  const view = await renderForm(async (_input, init) => {
    if (init?.method === 'POST') {
      const body = JSON.parse(String(init.body));
      writes.push(body);
      return json(
        { ...endpointWire, connector_type: 'typesafe-systemone', native_services: ['systemone'] },
        201,
      );
    }
    return json(supplied);
  });
  await view.user.selectOptions(
    screen.getByRole('combobox', { name: 'Protocol' }),
    'typesafe-systemone',
  );
  await view.user.type(
    screen.getByRole('textbox', { name: 'Endpoint URL' }),
    'https://system.example/v1',
  );
  expect(screen.queryByRole('checkbox', { name: 'System One' })).not.toBeInTheDocument();
  await view.user.click(screen.getByRole('button', { name: 'Save selected entries' }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0]).toMatchObject({
    connector_type: 'typesafe-systemone',
    native_services: ['systemone'],
  });
});
