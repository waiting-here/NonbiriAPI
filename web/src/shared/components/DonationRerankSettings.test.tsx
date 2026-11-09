import { useState } from 'react';
import { screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '@shared/query/http';
import type { DonationUse, UseRole } from '@shared/operations/donationUses';
import { renderWithProviders } from '../../../test/unit/support';
import { DonationRerankSettings } from './DonationRerankSettings';

const mocks = vi.hoisted(() => ({ mutate: vi.fn() }));
vi.mock('@shared/operations/donationUses', async (original) => ({
  ...(await original<typeof import('@shared/operations/donationUses')>()),
  mutateDonationUse: mocks.mutate,
}));
function fixture(): DonationUse {
  return {
    id: '6',
    donation_id: '3',
    donation_key_id: '4',
    endpoint_key_id: null,
    native_service: 'rerank',
    enabled: true,
    discovery_enabled: false,
    revision: '9',
    rerank_path_override: null,
    effective_rerank_path: '/donated/reranks',
    effective_rerank_url: 'https://api.example/donated/reranks',
    default_rerank_url: 'https://api.example/compatible/v1/rerank',
    approved: {
      credential_id: '8',
      credential_version: '1',
      grant_revision: '1',
      status: 'approved',
      review_note: '',
      reviewed_at: null,
      expected_review_revision: null,
      discovery_source_id: null,
      discovery_source_revision: null,
      display_head: 'head',
      display_tail: 'tail',
      review: { required: false, revision: null, material_available: true },
      discovery: null,
      rerank_path: '/donated/reranks',
      rerank_url: 'https://api.example/donated/reranks',
    },
    pending: null,
    latest: null,
    health: null,
    capabilities: {
      can_review: true,
      can_manage: true,
      can_edit_rerank_path: true,
      can_reset_failures: false,
      can_discover: false,
      can_bind: true,
      unavailable_reason: null,
    },
  };
}
beforeEach(() => mocks.mutate.mockReset().mockResolvedValue({}));
async function setup(role: UseRole = 'admin', initial = fixture()) {
  let server = initial;
  const refresh = vi.fn();
  function Harness() {
    const [row, setRow] = useState(server);
    return (
      <DonationRerankSettings
        role={role}
        donationId="3"
        use={row}
        refresh={async () => {
          refresh();
          const input = mocks.mutate.mock.calls.at(-1)?.[4];
          if (input)
            server = {
              ...server,
              rerank_path_override: input.rerank_path_override,
              revision: String(Number(server.revision) + 1),
            };
          setRow(server);
        }}
      />
    );
  }
  const view = await renderWithProviders(<Harness />, {
    station: role === 'admin' ? 'admin' : 'user',
  });
  view.queryClient.setQueryData(
    role === 'admin' ? ['admin', 'session'] : ['user', 'session'],
    role === 'admin'
      ? { admin: { username: 'fixture' } }
      : { user: { id: '1', username: 'fixture', effective_level: role === 'steward' ? 5 : 1 } },
  );
  return { ...view, refresh };
}
describe('charity rerank overrides', () => {
  it('adopts refreshed settings only while the editor has no local draft', async () => {
    const initial = fixture();
    const refresh = vi.fn();
    const view = await renderWithProviders(
      <DonationRerankSettings role="admin" donationId="3" use={initial} refresh={refresh} />,
      { station: 'admin' },
    );
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture' } });
    const updated = {
      ...initial,
      revision: '10',
      rerank_path_override: '/external/reranks',
      effective_rerank_url: 'https://api.example/external/reranks',
      effective_rerank_path: '/external/reranks',
    };
    view.rerender(
      <DonationRerankSettings role="admin" donationId="3" use={updated} refresh={refresh} />,
    );
    const path = await screen.findByRole('textbox');
    expect(path).toHaveValue('/external/reranks');
    await view.user.clear(path);
    await view.user.type(path, '/draft/reranks');
    view.rerender(
      <DonationRerankSettings
        role="admin"
        donationId="3"
        use={{ ...updated, revision: '11', rerank_path_override: '/another/reranks' }}
        refresh={refresh}
      />,
    );
    expect(path).toHaveValue('/draft/reranks');
    await view.user.click(screen.getByRole('button', { name: 'Save charity rerank interface' }));
    expect(mocks.mutate.mock.calls[0]![4]).toEqual({
      expected_revision: '10',
      rerank_path_override: '/draft/reranks',
    });
  });
  it.each(['admin', 'steward'] as const)(
    'saves a %s override, the standard suffix and restored inheritance with current revisions',
    async (role) => {
      const view = await setup(role);
      const source = screen.getByRole('combobox', { name: 'Interface configuration' });
      expect(source).toHaveValue('inherit');
      await view.user.selectOptions(source, 'custom');
      const path = screen.getByRole('textbox', { name: /Rerank interface path/ });
      await view.user.clear(path);
      await view.user.type(path, '/compatible-api/v1/reranks');
      expect(screen.getByText('https://api.example/compatible-api/v1/reranks')).toBeVisible();
      const save = screen.getByRole('button', { name: 'Save charity rerank interface' });
      await view.user.click(save);
      await waitFor(() => expect(save).toBeDisabled());
      expect(mocks.mutate.mock.calls[0]!.slice(0, 5)).toEqual([
        role,
        '3',
        '6',
        'patch',
        { expected_revision: '9', rerank_path_override: '/compatible-api/v1/reranks' },
      ]);
      await view.user.clear(path);
      expect(screen.getByText('https://api.example/compatible/v1/rerank')).toBeVisible();
      await view.user.click(save);
      await waitFor(() => expect(save).toBeDisabled());
      expect(mocks.mutate.mock.calls[1]![4]).toEqual({
        expected_revision: '10',
        rerank_path_override: '',
      });
      await view.user.selectOptions(source, 'inherit');
      expect(screen.getByText('https://api.example/donated/reranks')).toBeVisible();
      await view.user.click(save);
      await waitFor(() => expect(save).toBeDisabled());
      expect(mocks.mutate.mock.calls[2]![4]).toEqual({
        expected_revision: '11',
        rerank_path_override: null,
      });
    },
  );
  it('retains the draft and one operation identity when an unknown result is checked', async () => {
    const view = await setup();
    let attempts = 0;
    mocks.mutate.mockImplementation(async () => {
      if (++attempts < 3) throw new ApiError('network_error', 'Response lost', 0);
      return {};
    });
    await view.user.selectOptions(screen.getByRole('combobox'), 'custom');
    const path = screen.getByRole('textbox');
    await view.user.clear(path);
    await view.user.type(path, '/retained/reranks');
    await view.user.click(screen.getByRole('button', { name: 'Save charity rerank interface' }));
    expect(await screen.findByRole('button', { name: 'Check saved result' })).toBeVisible();
    expect(path).toHaveValue('/retained/reranks');
    await view.user.click(screen.getByRole('button', { name: 'Check saved result' }));
    await screen.findByText('Saved.');
    expect(mocks.mutate.mock.calls).toHaveLength(3);
    expect(new Set(mocks.mutate.mock.calls.map((call) => call[5])).size).toBe(1);
    expect(path).toHaveValue('/retained/reranks');
  });
  it.each(['owner', 'steward'] as const)(
    'shows the effective URL without editing authority for %s',
    async (role) => {
      const row = fixture();
      row.capabilities.can_edit_rerank_path = false;
      await setup(role, row);
      expect(screen.getByText(row.effective_rerank_url!)).toBeVisible();
      expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
      expect(screen.queryByRole('button')).not.toBeInTheDocument();
    },
  );
});
