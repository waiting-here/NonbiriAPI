import { useEffect } from 'react';
import { act, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import * as charity from '@shared/operations/charity';
import * as modelPages from '@shared/operations/charityModelPages';
import { ApiError } from '@shared/query/http';
import type { CharityBindingPicker, CharitySelection } from '../CharityBindingPicker';
import type { ManagedDonationKeySummary } from '@shared/operations/donationPages';
import { renderWithProviders } from '../../../../test/unit/support';
import { MultiTargetBindingWorkflow } from './MultiTargetBindingWorkflow';
vi.mock('../AsyncLeaveDialog', () => ({ AsyncLeaveDialog: () => null }));

vi.mock('../CharityBindingPicker', () => ({
  CharityBindingPicker: ({
    modelId,
    capacity,
    selected,
    onChange,
    locked,
    onReadStateChange,
  }: Parameters<typeof CharityBindingPicker>[0]) => {
    useEffect(() => onReadStateChange?.(false), [onReadStateChange]);
    const candidate: CharitySelection = {
      donation_id: '7',
      donation_key_id: '21',
      upstream_model_id: `up-${modelId}`,
      source_types: ['automatic'],
      source: {
        connector_type: 'openai-compatible',
        canonical_base_url: 'https://example.test',
        display_head: 'head',
        display_tail: 'tail',
      },
      note: '',
    };
    return (
      <>
        <label>
          <input
            type="checkbox"
            checked={Boolean(selected[`21:up-${modelId}`])}
            disabled={locked}
            onChange={(event) =>
              onChange(event.target.checked ? { [`21:up-${modelId}`]: candidate } : {})
            }
          />
          Upstream for {modelId}
        </label>
        <button
          type="button"
          disabled={locked}
          onClick={() =>
            onChange({
              ...selected,
              [`21:extra-${modelId}`]: { ...candidate, upstream_model_id: `extra-${modelId}` },
            })
          }
        >
          Select another upstream
        </button>
        <button
          type="button"
          disabled={locked || (capacity ?? 256) < 144}
          onClick={() =>
            onChange(
              Object.fromEntries(
                Array.from({ length: 144 }, (_, index) => {
                  const id = `bulk-${String(index).padStart(3, '0')}`;
                  return [`21:${id}`, { ...candidate, upstream_model_id: id }];
                }),
              ),
            )
          }
        >
          Select 144 upstream models
        </button>
      </>
    );
  },
}));
const item = {
  key_id: '21',
  donation_id: '7',
  donation_revision: '4',
  safe_note: 'Primary donation',
  charity_state: 'available',
} as ManagedDonationKeySummary;
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status, headers: { 'content-type': 'application/json' } });
const options = {
  data: [
    { charity_model_id: '31', full_name: 'Target A', enabled: true, binding_revision: '1' },
    { charity_model_id: '32', full_name: 'Target B', enabled: true, binding_revision: '3' },
  ],
  next_cursor: null,
  pagination: { page: '1', page_size: 20, total_items: '2', total_pages: '1' },
};
async function setup(execute: (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>) {
  vi.stubGlobal('fetch', vi.fn<typeof fetch>(execute));
  const view = await renderWithProviders(
    <MultiTargetBindingWorkflow
      role="admin"
      accountId="fixture-admin"
      item={item}
      onBack={vi.fn()}
      onCreate={vi.fn()}
    />,
    { station: 'admin' },
  );
  view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } });
  return view;
}
async function choose(view: Awaited<ReturnType<typeof setup>>, name: string, id: string) {
  await view.user.click(
    within(await screen.findByRole('row', { name: new RegExp(name) })).getByRole('button', {
      name: 'Choose upstream models',
    }),
  );
  await view.user.click(await screen.findByRole('checkbox', { name: `Upstream for ${id}` }));
  await view.user.click(screen.getByRole('button', { name: 'Choose another charity model' }));
}
describe('multi-target donation binding recovery', () => {
  it('rereads at most four targets together and keeps deleted targets removable in order', async () => {
    const rows = Array.from({ length: 8 }, (_, index) => ({
      charity_model_id: String(31 + index),
      full_name: `Target ${index + 1}`,
      enabled: true,
      binding_revision: '1',
    }));
    const queued: (() => void)[] = [];
    let active = 0;
    let peak = 0;
    vi.spyOn(charity, 'getManagedDonation').mockResolvedValue({
      revision: '5',
    } as Awaited<ReturnType<typeof charity.getManagedDonation>>);
    vi.spyOn(modelPages, 'getManagedCharityModel').mockImplementation(
      (_role, id) =>
        new Promise((resolve, reject) => {
          peak = Math.max(peak, ++active);
          queued.push(() => {
            active--;
            if (id === '31') reject(new ApiError('not_found', 'Deleted target', 404));
            else
              resolve({ id, binding_revision: '2' } as Awaited<
                ReturnType<typeof modelPages.getManagedCharityModel>
              >);
          });
        }),
    );
    const view = await setup(async (_input, init) =>
      (init?.method ?? 'GET') === 'GET'
        ? json({ ...options, data: rows, pagination: { ...options.pagination, total_items: '8' } })
        : json({ error: { code: 'conflict', message: 'Changed bindings' } }, 409),
    );
    for (const row of rows) await choose(view, row.full_name, row.charity_model_id);
    await view.user.click(screen.getByRole('button', { name: 'Add 8 connections' }));
    await view.user.click(
      await screen.findByRole('button', { name: 'Refresh and keep selections' }),
    );
    for (let completed = 0; completed < rows.length; completed += 4) {
      await waitFor(() => expect(queued).toHaveLength(4));
      expect(active).toBe(4);
      await act(async () => queued.splice(0).forEach((finish) => finish()));
    }
    const selected = within(screen.getByRole('region', { name: 'Selected connections' }));
    await waitFor(() => expect(selected.getByText(/Target 1 ·/)).toBeInTheDocument());
    expect(peak).toBe(4);
    expect(
      selected
        .getAllByRole('row')
        .slice(1)
        .map((row) => row.textContent),
    ).toEqual(
      rows.map((row, index) =>
        expect.stringContaining(`${row.full_name}${index === 0 ? ' ·' : ''}`),
      ),
    );
    expect(screen.getByRole('button', { name: 'Add 8 connections' })).toBeDisabled();
    await view.user.click(
      selected.getByRole('button', { name: 'Remove selected Target 1: up-31' }),
    );
    expect(screen.getByRole('button', { name: 'Add 7 connections' })).toBeEnabled();
  });
  it('keeps the complete draft when a target reread fails', async () => {
    vi.spyOn(charity, 'getManagedDonation').mockResolvedValue({
      revision: '5',
    } as Awaited<ReturnType<typeof charity.getManagedDonation>>);
    vi.spyOn(modelPages, 'getManagedCharityModel').mockRejectedValue(
      new TypeError('Target read failed'),
    );
    const view = await setup(async (_input, init) =>
      (init?.method ?? 'GET') === 'GET'
        ? json(options)
        : json({ error: { code: 'conflict', message: 'Changed bindings' } }, 409),
    );
    await choose(view, 'Target A', '31');
    await choose(view, 'Target B', '32');
    await view.user.click(screen.getByRole('button', { name: 'Add 2 connections' }));
    await view.user.click(
      await screen.findByRole('button', { name: 'Refresh and keep selections' }),
    );
    await waitFor(() => expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled());
    const selected = within(screen.getByRole('region', { name: 'Selected connections' }));
    expect(selected.getByText('Target A')).toBeInTheDocument();
    expect(selected.getByText('Target B')).toBeInTheDocument();
    expect(selected.getAllByRole('button', { name: /Remove selected/ })).toHaveLength(2);
    expect(screen.getByRole('button', { name: 'Add 2 connections' })).toBeDisabled();
  });
  it('keeps 144 selections searchable and paginated, including removal and reselection', async () => {
    const view = await setup(async () => json(options));
    await view.user.click(
      within(await screen.findByRole('row', { name: /Target A/ })).getByRole('button', {
        name: 'Choose upstream models',
      }),
    );
    await view.user.click(
      await screen.findByRole('button', { name: 'Select 144 upstream models' }),
    );
    await view.user.click(screen.getByRole('button', { name: 'Choose another charity model' }));
    expect(screen.getByRole('button', { name: 'Add 144 connections' })).toBeEnabled();
    const selected = within(screen.getByRole('region', { name: 'Selected connections' }));
    await view.user.selectOptions(selected.getByRole('combobox'), '100');
    expect(selected.getAllByRole('row')).toHaveLength(101);
    await view.user.click(selected.getByRole('button', { name: 'Next' }));
    expect(selected.getAllByRole('row')).toHaveLength(45);
    const search = selected.getByRole('searchbox', {
      name: 'Search selected models and upstream IDs',
    });
    await view.user.type(search, 'bulk-143');
    await view.user.click(
      selected.getByRole('button', { name: 'Remove selected Target A: bulk-143' }),
    );
    expect(search).toHaveFocus();
    expect(screen.getByRole('button', { name: 'Add 143 connections' })).toBeEnabled();
    await view.user.clear(search);
    await view.user.type(search, 'Target A');
    expect(selected.getAllByRole('row')).toHaveLength(101);
    await view.user.click(
      within(
        within(screen.getByRole('table', { name: 'Choose this model' })).getByRole('row', {
          name: /Target A/,
        }),
      ).getByRole('button', { name: 'Choose upstream models' }),
    );
    await view.user.click(screen.getByRole('button', { name: 'Select 144 upstream models' }));
    expect(screen.getByRole('button', { name: 'Add 144 connections' })).toBeEnabled();
  });
  it('uses the returned revision for unfinished items on the same partially successful target', async () => {
    const writes: { targets: { expected_binding_revision: string }[] }[] = [];
    const view = await setup(async (_input, init) => {
      if ((init?.method ?? 'GET') === 'GET') return json(options);
      writes.push(JSON.parse(String(init?.body)));
      return json({
        items: [
          {
            donation_key_id: '21',
            results: writes.length === 1 ? ['added', 'missing_model'] : ['missing_model'],
          },
        ],
        models: [{ charity_model_id: '31', binding_revision: '2' }],
        added_count: writes.length === 1 ? '1' : '0',
      });
    });
    await view.user.click(
      within(await screen.findByRole('row', { name: /Target A/ })).getByRole('button', {
        name: 'Choose upstream models',
      }),
    );
    await view.user.click(await screen.findByRole('checkbox', { name: 'Upstream for 31' }));
    await view.user.click(screen.getByRole('button', { name: 'Select another upstream' }));
    await view.user.click(screen.getByRole('button', { name: 'Choose another charity model' }));
    await view.user.click(screen.getByRole('button', { name: 'Add 2 connections' }));
    await view.user.click(await screen.findByRole('button', { name: 'Add 1 connections' }));
    await screen.findByText('Upstream model not found');
    expect(writes.map((write) => write.targets[0]?.expected_binding_revision)).toEqual(['1', '2']);
  });
  it('retains both targets while navigating back, and retries an unknown result with the same snapshot', async () => {
    const writes: { body: unknown; key: string | null }[] = [];
    const view = await setup(async (_input, init) => {
      if ((init?.method ?? 'GET') === 'GET') return json(options);
      writes.push({
        body: JSON.parse(String(init?.body)),
        key: new Headers(init?.headers).get('Idempotency-Key'),
      });
      if (writes.length === 1) throw new TypeError('connection lost');
      return json({
        items: [{ donation_key_id: '21', results: ['added', 'missing_model'] }],
        models: [{ charity_model_id: '31', binding_revision: '2' }],
        added_count: '1',
      });
    });
    await choose(view, 'Target A', '31');
    await choose(view, 'Target B', '32');
    await view.user.click(screen.getByRole('button', { name: 'Add 2 connections' }));
    await view.user.click(await screen.findByRole('button', { name: 'Retry the original action' }));
    await screen.findByText('Upstream model not found');
    expect(writes).toHaveLength(2);
    expect(writes[1]).toEqual(writes[0]);
    expect(writes[0]?.body).toEqual({
      expected_donation_revision: '4',
      targets: [
        { charity_model_id: '31', expected_binding_revision: '1', upstream_model_id: 'up-31' },
        { charity_model_id: '32', expected_binding_revision: '3', upstream_model_id: 'up-32' },
      ],
    });
    expect(screen.getByRole('button', { name: 'Add 1 connections' })).toBeEnabled();
    expect(
      within(screen.getByRole('region', { name: 'Selected connections' })).queryByText('Target A'),
    ).not.toBeInTheDocument();
  });
  it('refreshes a confirmed action without writing again when its following read failed', async () => {
    let readsFail = false,
      writes = 0;
    const view = await setup(async (_input, init) => {
      if ((init?.method ?? 'GET') === 'GET') {
        if (readsFail) throw new TypeError('read failed');
        return json(options);
      }
      writes++;
      readsFail = true;
      return json({
        items: [{ donation_key_id: '21', results: ['added'] }],
        models: [{ charity_model_id: '31', binding_revision: '2' }],
        added_count: '1',
      });
    });
    await choose(view, 'Target A', '31');
    await view.user.click(screen.getByRole('button', { name: 'Add 1 connections' }));
    const refresh = await screen.findByRole('button', { name: 'Check again' });
    expect(
      screen.queryByRole('button', { name: 'Retry the original action' }),
    ).not.toBeInTheDocument();
    readsFail = false;
    await view.user.click(refresh);
    expect(writes).toBe(1);
  });
});
