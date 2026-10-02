import { act, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import {
  beginManagementSessionRequest,
  noteManagementSessionSuccess,
} from '@shared/charityManagement';
import { GatewayCapabilitySummary } from '@shared/gateway/GatewayCapabilitySummary';
import { renderWithProviders } from '../../../../test/unit/support';
import GatewayCapabilitiesSection from './GatewayCapabilitiesSection';
import type { GatewayCapabilityEntry, GatewayCapabilityRecord } from './api';

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
      await user.click(screen.getByRole('button', { name: i18n.t('common.save') }));
      await waitFor(() =>
        expect(document.querySelectorAll('.nb-operation-feedback--confirmed')).toHaveLength(1),
      );
      expect(writes).toHaveLength(1);
      expect(writes[0].json).toMatchObject({
        expected_revision: '0',
        entry: { max_output_tokens: 0, storage: 'reject', cache: 'reject' },
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
