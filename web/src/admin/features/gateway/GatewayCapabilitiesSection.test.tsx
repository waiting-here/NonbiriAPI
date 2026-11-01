import { act, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import {
  beginManagementSessionRequest,
  noteManagementSessionSuccess,
} from '@shared/charityManagement';
import { GatewayCapabilitySummary } from '@shared/gateway/GatewayCapabilitySummary';
import { renderWithProviders } from '../../../../test/unit/support';
import GatewayCapabilitiesSection from './GatewayCapabilitiesSection';
import {
  gatewayCapabilityKeys,
  type GatewayCapabilityEntry,
  type GatewayCapabilityRecord,
} from './api';

const entry: GatewayCapabilityEntry = {
  base_url: 'https://gateway.example/v3',
  model: 'provider/model',
  adapter: 'anthropic_always_adaptive',
  efforts: ['low', 'xhigh', 'max'],
  max_output_tokens: 128000,
  storage: 'reject',
  cache: 'anthropic',
};
const row: GatewayCapabilityRecord = { ...entry, id: '12', revision: '3', updated_at: 1800000000 };
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });

async function renderSection(locale: 'en' | 'zh' = 'en') {
  const view = await renderWithProviders(<div />, {
    station: 'admin',
    role: 'admin',
    locale,
  });
  const session = { admin: { username: 'fixture-admin' } };
  await act(async () => {
    const generation = beginManagementSessionRequest(view.queryClient, 'admin');
    noteManagementSessionSuccess(view.queryClient, 'admin', session, generation);
    view.queryClient.setQueryData(['admin', 'session'], session);
  });
  view.rerender(<GatewayCapabilitiesSection />);
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: view.i18n.t('gatewayCapabilities.add') }),
    ).toBeEnabled(),
  );
  return view;
}

describe('Gateway capability management', () => {
  it.each(['en', 'zh'] as const)(
    'saves once without confirmation and clears success on the next edit (%s)',
    async (locale) => {
      let rows: GatewayCapabilityRecord[] = [];
      const writes: { json: unknown; key: string | null }[] = [];
      vi.stubGlobal(
        'fetch',
        vi.fn(async (_input: unknown, init?: RequestInit) => {
          if (!init?.method || init.method === 'GET') return json({ data: rows });
          const body = JSON.parse(String(init.body));
          writes.push({ json: body, key: new Headers(init.headers).get('Idempotency-Key') });
          rows = [{ ...body.entry, id: '12', revision: '1', updated_at: 1800000000 }];
          return json(rows[0]);
        }),
      );
      const { user, i18n } = await renderSection(locale);
      await user.click(screen.getByRole('button', { name: i18n.t('gatewayCapabilities.add') }));
      await user.type(screen.getByLabelText(i18n.t('gatewayCapabilities.baseUrl')), entry.base_url);
      await user.type(screen.getByLabelText(i18n.t('gatewayCapabilities.model')), entry.model);
      await user.selectOptions(
        screen.getByLabelText(i18n.t('gatewayCapabilities.adapter')),
        'anthropic_always_adaptive',
      );
      await user.selectOptions(
        screen.getByLabelText(i18n.t('gatewayCapabilities.cache')),
        'anthropic_explicit',
      );
      await user.clear(screen.getByLabelText(i18n.t('gatewayCapabilities.output')));
      await user.type(screen.getByLabelText(i18n.t('gatewayCapabilities.output')), '1024');
      await user.click(screen.getByRole('button', { name: i18n.t('common.save') }));
      await waitFor(() =>
        expect(document.querySelectorAll('.nb-operation-feedback--confirmed')).toHaveLength(1),
      );
      expect(writes).toHaveLength(1);
      expect(writes[0].json).toMatchObject({
        expected_revision: '0',
        entry: { max_output_tokens: 1024, storage: 'reject', cache: 'anthropic_explicit' },
      });
      expect(writes[0].key).toMatch(/^[A-Za-z0-9_-]{22}$/);
      expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
      await user.type(screen.getByLabelText(i18n.t('gatewayCapabilities.model')), '-changed');
      expect(document.querySelector('.nb-operation-feedback')).not.toBeInTheDocument();
      expect(screen.getByLabelText(i18n.t('gatewayCapabilities.model'))).toHaveValue(
        'provider/model-changed',
      );
    },
  );

  it('checks an unknown write with its original intent and key, then updates using the returned revision', async () => {
    let rows: GatewayCapabilityRecord[] = [];
    const writes: { method: string; body: Record<string, unknown>; key: string | null }[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_input: unknown, init?: RequestInit) => {
        if (!init?.method || init.method === 'GET') return json({ data: rows });
        const body = JSON.parse(String(init.body));
        writes.push({
          method: init.method,
          body,
          key: new Headers(init.headers).get('Idempotency-Key'),
        });
        if (writes.length === 1) throw new Error('response lost');
        rows = [
          { ...body.entry, id: '12', revision: String(writes.length - 1), updated_at: 1800000000 },
        ];
        return json(rows[0]);
      }),
    );
    const { user, i18n } = await renderSection();
    await user.click(screen.getByRole('button', { name: i18n.t('gatewayCapabilities.add') }));
    await user.type(screen.getByLabelText(i18n.t('gatewayCapabilities.baseUrl')), entry.base_url);
    await user.type(screen.getByLabelText(i18n.t('gatewayCapabilities.model')), entry.model);
    await user.click(screen.getByRole('button', { name: i18n.t('common.save') }));
    await waitFor(() =>
      expect(document.querySelector('.nb-operation-feedback--unknown')).toBeInTheDocument(),
    );
    expect(screen.getByLabelText(i18n.t('gatewayCapabilities.model'))).toHaveValue(entry.model);
    await user.click(screen.getByRole('button', { name: i18n.t('common.operation.check') }));
    await waitFor(() =>
      expect(document.querySelector('.nb-operation-feedback--confirmed')).toBeInTheDocument(),
    );
    expect(writes[1].key).toBe(writes[0].key);
    expect(writes[1].body).toEqual(writes[0].body);
    await user.type(screen.getByLabelText(i18n.t('gatewayCapabilities.model')), '-edit');
    await user.click(screen.getByRole('button', { name: i18n.t('common.save') }));
    await waitFor(() => expect(writes).toHaveLength(3));
    expect(writes[2]).toMatchObject({ method: 'PUT', body: { expected_revision: '1' } });
    expect(writes[2].key).not.toBe(writes[0].key);
  });

  it('keeps compatible controls and failed input, and checks a lost delete after one confirmation', async () => {
    const requests: RequestInit[] = [];
    let rows = [row];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_input: unknown, init?: RequestInit) => {
        if (!init?.method || init.method === 'GET') return json({ data: rows });
        requests.push(init);
        if (init.method === 'PUT')
          return json(
            {
              error: { code: 'invalid_request', message: 'Check the verified model capabilities.' },
            },
            400,
          );
        rows = [];
        if (requests.filter((request) => request.method === 'DELETE').length === 1)
          throw new Error('delete response lost');
        return json({ id: row.id, deleted: true });
      }),
    );
    const { user, i18n } = await renderSection();
    await user.click(
      screen.getByRole('button', {
        name: i18n.t('gatewayCapabilities.editTarget', { model: row.model }),
      }),
    );
    const form = screen.getByRole('button', { name: i18n.t('common.save') }).closest('form')!;
    expect(
      within(form).queryByRole('checkbox', { name: i18n.t('gatewayCapabilities.effort.none') }),
    ).not.toBeInTheDocument();
    expect(
      within(form).getByRole('checkbox', { name: i18n.t('gatewayCapabilities.effort.xhigh') }),
    ).toBeChecked();
    expect(
      within(form).queryByRole('option', {
        name: i18n.t('gatewayCapabilities.storageOptions.openai'),
      }),
    ).not.toBeInTheDocument();
    const output = screen.getByLabelText(i18n.t('gatewayCapabilities.output'));
    await user.clear(output);
    await user.type(output, '256');
    await user.click(screen.getByRole('button', { name: i18n.t('common.save') }));
    await screen.findByText('Check the verified model capabilities.');
    expect(output).toHaveValue(256);
    await user.selectOptions(
      screen.getByLabelText(i18n.t('gatewayCapabilities.adapter')),
      'openai_chat',
    );
    expect(form.querySelector('details')).toHaveAttribute('open');
    expect(
      within(form).queryByRole('checkbox', { name: i18n.t('gatewayCapabilities.effort.max') }),
    ).not.toBeInTheDocument();
    expect(
      within(form).getByRole('checkbox', { name: i18n.t('gatewayCapabilities.effort.xhigh') }),
    ).toBeChecked();
    await user.click(
      screen.getByRole('button', {
        name: i18n.t('gatewayCapabilities.deleteTarget', { model: row.model }),
      }),
    );
    expect(requests.filter((request) => request.method === 'DELETE')).toHaveLength(0);
    const dialog = screen.getByRole('alertdialog');
    await user.click(within(dialog).getByRole('button', { name: i18n.t('common.remove') }));
    await waitFor(() =>
      expect(dialog.querySelector('.nb-operation-feedback--unknown')).toBeInTheDocument(),
    );
    await user.click(
      within(dialog).getByRole('button', { name: i18n.t('common.operation.check') }),
    );
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument());
    const deletions = requests.filter((request) => request.method === 'DELETE');
    expect(deletions).toHaveLength(2);
    expect(JSON.parse(String(deletions[0].body))).toEqual({ expected_revision: '3' });
    expect(new Headers(deletions[0].headers).get('Idempotency-Key')).toBeTruthy();
    expect(new Headers(deletions[1].headers).get('Idempotency-Key')).toBe(
      new Headers(deletions[0].headers).get('Idempotency-Key'),
    );
    expect(screen.queryByRole('button', { name: i18n.t('common.save') })).not.toBeInTheDocument();
  });

  it('renders a scoped read-only summary without loading the administrator list', async () => {
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    const { i18n, rerender } = await renderWithProviders(
      <GatewayCapabilitySummary entry={entry} />,
      { station: 'user', role: 'level6' },
    );
    expect(
      screen.getByText(i18n.t('gatewayCapabilities.adapters.anthropic_always_adaptive')),
    ).toBeInTheDocument();
    rerender(<GatewayCapabilitySummary entry={null} />);
    expect(screen.getByText(i18n.t('gatewayCapabilities.absent'))).toBeInTheDocument();
    expect(fetch).not.toHaveBeenCalled();
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });
});

it('retains new and per-record Gateway drafts and cancels only the current draft', async () => {
  const other = { ...row, id: '13', model: 'provider/other' };
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => json({ data: [row, other] })),
  );
  const { user, i18n } = await renderSection();
  const add = () => screen.getByRole('button', { name: i18n.t('gatewayCapabilities.add') });
  const edit = (index: number) =>
    screen.getByRole('button', {
      name: i18n.t('gatewayCapabilities.editTarget', { model: [row, other][index].model }),
    });
  const model = () => screen.getByLabelText(i18n.t('gatewayCapabilities.model'));
  await user.click(add());
  await user.type(
    screen.getByLabelText(i18n.t('gatewayCapabilities.baseUrl')),
    'https://draft.example/v1',
  );
  await user.type(model(), 'new-model');
  await user.click(add());
  expect(model()).toHaveValue('new-model');
  await user.click(edit(0));
  await user.type(model(), '-edited');
  await user.click(edit(1));
  await user.type(model(), '-edited');
  await user.click(edit(0));
  expect(model()).toHaveValue('provider/model-edited');
  await user.click(edit(0));
  expect(model()).toHaveValue('provider/model-edited');
  await user.click(screen.getByRole('button', { name: i18n.t('common.cancel') }));
  await user.click(edit(0));
  expect(model()).toHaveValue('provider/model');
  await user.click(edit(1));
  expect(model()).toHaveValue('provider/other-edited');
  await user.click(add());
  expect(model()).toHaveValue('new-model');
  expect(screen.getByLabelText(i18n.t('gatewayCapabilities.baseUrl'))).toHaveValue(
    'https://draft.example/v1',
  );
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
});

it('refreshes clean Gateway records while keeping dirty revisions and advanced disclosure state', async () => {
  let current: GatewayCapabilityRecord = { ...row, cache: 'reject' };
  const writes: unknown[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_input: unknown, init?: RequestInit) => {
      if (!init?.method || init.method === 'GET') return json({ data: [current] });
      writes.push(JSON.parse(String(init.body)));
      return json({ error: { code: 'conflict', message: 'The record changed.' } }, 409);
    }),
  );
  const { user, i18n, queryClient } = await renderSection();
  await user.click(
    screen.getByRole('button', {
      name: i18n.t('gatewayCapabilities.editTarget', { model: row.model }),
    }),
  );
  const advanced = () =>
    screen.getByText(i18n.t('gatewayCapabilities.advanced')).closest('details');
  expect(advanced()).not.toHaveAttribute('open');
  current = { ...current, revision: '4', model: 'refreshed-model', cache: 'anthropic' };
  await act(async () => {
    queryClient.setQueryData(gatewayCapabilityKeys.root, [current]);
  });
  await waitFor(() =>
    expect(screen.getByLabelText(i18n.t('gatewayCapabilities.model'))).toHaveValue(
      'refreshed-model',
    ),
  );
  expect(advanced()).toHaveAttribute('open');
  await user.click(screen.getByText(i18n.t('gatewayCapabilities.advanced')));
  await user.type(screen.getByLabelText(i18n.t('gatewayCapabilities.model')), '-edited');
  expect(advanced()).not.toHaveAttribute('open');
  current = { ...current, revision: '5', model: 'server-model' };
  await act(async () => {
    queryClient.setQueryData(gatewayCapabilityKeys.root, [current]);
  });
  await user.click(screen.getByRole('button', { name: i18n.t('common.save') }));
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0]).toMatchObject({
    expected_revision: '4',
    entry: { model: 'refreshed-model-edited' },
  });
  expect(screen.getByLabelText(i18n.t('gatewayCapabilities.model'))).toHaveValue(
    'refreshed-model-edited',
  );
});

it('drops Gateway drafts when the cached administrator changes', async () => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => json({ data: [row] })),
  );
  const view = await renderSection();
  await view.user.click(
    screen.getByRole('button', { name: view.i18n.t('gatewayCapabilities.add') }),
  );
  await view.user.type(
    screen.getByLabelText(view.i18n.t('gatewayCapabilities.model')),
    'private-draft',
  );
  await view.user.click(
    screen.getByRole('button', {
      name: view.i18n.t('gatewayCapabilities.editTarget', { model: row.model }),
    }),
  );
  await act(async () => {
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'another-admin' } });
  });
  await waitFor(() =>
    expect(
      screen.queryByLabelText(view.i18n.t('gatewayCapabilities.model')),
    ).not.toBeInTheDocument(),
  );
  await view.user.click(
    screen.getByRole('button', { name: view.i18n.t('gatewayCapabilities.add') }),
  );
  expect(screen.getByLabelText(view.i18n.t('gatewayCapabilities.model'))).toHaveValue('');
});

it('keeps the confirmed Gateway revision when the following list refresh fails', async () => {
  const writes: Record<string, unknown>[] = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_input: unknown, init?: RequestInit) => {
      if (!init?.method || init.method === 'GET') {
        if (writes.length) throw new Error('List refresh unavailable');
        return json({ data: [row] });
      }
      const body = JSON.parse(String(init.body));
      writes.push(body);
      return json({ ...row, ...body.entry, revision: String(3 + writes.length) });
    }),
  );
  const { user, i18n } = await renderSection();
  await user.click(
    screen.getByRole('button', {
      name: i18n.t('gatewayCapabilities.editTarget', { model: row.model }),
    }),
  );
  await user.type(screen.getByLabelText(i18n.t('gatewayCapabilities.model')), '-saved');
  await user.click(screen.getByRole('button', { name: i18n.t('common.save') }));
  await waitFor(() =>
    expect(document.querySelector('.nb-operation-feedback--refresh-failed')).toBeInTheDocument(),
  );
  expect(screen.getByLabelText(i18n.t('gatewayCapabilities.model'))).toHaveValue(
    row.model + '-saved',
  );
  await user.click(
    screen.getByRole('button', {
      name: i18n.t('gatewayCapabilities.editTarget', { model: row.model }),
    }),
  );
  expect(screen.getByLabelText(i18n.t('gatewayCapabilities.model'))).toHaveValue(
    row.model + '-saved',
  );
  await user.type(screen.getByLabelText(i18n.t('gatewayCapabilities.model')), '-again');
  await user.click(screen.getByRole('button', { name: i18n.t('common.save') }));
  await waitFor(() => expect(writes).toHaveLength(2));
  expect(writes[1]).toMatchObject({
    expected_revision: '4',
    entry: { model: row.model + '-saved-again' },
  });
});
