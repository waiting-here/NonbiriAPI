import { useState } from 'react';
import { screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { connectionAPI } from '../core/connections/api';
import { DonationShareFields, type DonationShare } from './DonationShareFields';
import type { DiscoverySource } from '../core/connections/types';
vi.mock('../core/connections/api', () => ({ connectionAPI: { source: vi.fn() } }));
const source: DiscoverySource = {
  id: '51',
  connection_id: '2',
  name: 'Catalog',
  base_url: 'https://catalog.example',
  models_url: 'https://catalog.example/models',
  list_format: 'openai',
  credential_id: '81',
  credential_version: '1',
  revision: '3',
  discovery_source_revision: '7',
  enabled: true,
  credential_available: true,
  state: 'succeeded',
  checked_at: 1,
  safe_class: 'none',
  safe_diag: '',
  created_at: 1,
  updated_at: 2,
};
function Draft({ observe }: { observe: (value: DonationShare) => void }) {
  const [share, setShare] = useState<DonationShare>({
    services: ['chat_completions', 'responses'],
    initializeSource: true,
  });
  return (
    <DonationShareFields
      accountId="1"
      choice={{
        key: {
          nativeServices: ['chat_completions', 'responses'],
          credentialId: '81',
          credentialVersion: '1',
          grantRevision: '1',
          discoverySourceId: '51',
        },
      }}
      value={share}
      disabled={false}
      onChange={(value) => {
        setShare(value);
        observe(value);
      }}
    />
  );
}
beforeEach(() => vi.mocked(connectionAPI.source).mockReset());
describe('donation directory defaults', () => {
  it('uses only the exact compatible catalog revision in a new draft', async () => {
    vi.mocked(connectionAPI.source).mockResolvedValue(source);
    const observe = vi.fn();
    await renderWithProviders(<Draft observe={observe} />, { station: 'user', role: 'user' });
    await waitFor(() =>
      expect(screen.getByRole('checkbox', { name: 'Share this catalog' })).toBeChecked(),
    );
    expect(observe).toHaveBeenLastCalledWith({
      services: ['chat_completions', 'responses'],
      initializeSource: false,
      sourceId: '51',
      sourceRevision: '7',
    });
  });
  it.each([
    { enabled: false },
    { credential_available: false },
    { credential_id: '82' },
    { credential_version: '2' },
  ])('keeps an incompatible catalog unchecked: %j', async (difference) => {
    vi.mocked(connectionAPI.source).mockResolvedValue({ ...source, ...difference });
    const observe = vi.fn();
    await renderWithProviders(<Draft observe={observe} />, { station: 'user', role: 'user' });
    await waitFor(() =>
      expect(observe).toHaveBeenCalledWith({
        services: ['chat_completions', 'responses'],
        initializeSource: false,
      }),
    );
    expect(screen.getByRole('checkbox', { name: 'Share this catalog' })).not.toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'Share this catalog' })).toBeDisabled();
  });
  it('finishes initialization after a failed read without sharing or silently selecting it after a refresh', async () => {
    vi.mocked(connectionAPI.source).mockRejectedValue(new Error('Catalog read failed'));
    const observe = vi.fn();
    const view = await renderWithProviders(<Draft observe={observe} />, {
      station: 'user',
      role: 'user',
    });
    await waitFor(() =>
      expect(observe).toHaveBeenCalledWith({
        services: ['chat_completions', 'responses'],
        initializeSource: false,
      }),
    );
    vi.mocked(connectionAPI.source).mockResolvedValue(source);
    await view.user.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() =>
      expect(screen.getByRole('checkbox', { name: 'Share this catalog' })).toBeEnabled(),
    );
    expect(screen.getByRole('checkbox', { name: 'Share this catalog' })).not.toBeChecked();
    await view.user.click(screen.getByRole('checkbox', { name: 'Share this catalog' }));
    expect(observe).toHaveBeenLastCalledWith({
      services: ['chat_completions', 'responses'],
      initializeSource: false,
      sourceId: '51',
      sourceRevision: '7',
    });
  });
});
