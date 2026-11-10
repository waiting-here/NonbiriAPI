import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { DISCOVERY_FORMATS } from '@shared/connections';
import common from '@shared/i18n/common/en.json';
import user from '../../../i18n/en.json';
import { renderWithProviders } from '../../../../../test/unit/support';
import { SourceForm } from './SourceForm';

const copy = common.common.connections;

describe('complete model list addresses', () => {
  it.each(DISCOVERY_FORMATS)('saves an explicit %s request URL unchanged', async (format) => {
    const address = 'https://catalog.example/custom/model-directory/';
    const writes: Record<string, unknown>[] = [];
    const saved = vi.fn();
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (url, init) => {
        expect(String(url)).toBe('/api/connections/9/discovery-sources');
        expect(init?.method).toBe('POST');
        const input = JSON.parse(String(init?.body));
        writes.push(input);
        return new Response(
          JSON.stringify({
            ...input,
            id: '51',
            connection_id: '9',
            models_url: input.base_url,
            credential_version: '2',
            revision: '1',
            discovery_source_revision: '1',
            state: 'unknown',
            checked_at: null,
            safe_class: 'none',
            safe_diag: '',
            created_at: 1,
            updated_at: 1,
          }),
          { status: 201, headers: { 'Content-Type': 'application/json' } },
        );
      }),
    );
    const view = await renderWithProviders(
      <SourceForm
        accountId="7"
        connectionId="9"
        fixedCredentialId="8"
        pairSettings
        preset={{ base_url: address, list_format: format }}
        onSaved={saved}
      />,
      { station: 'user', role: 'user' },
    );
    expect(screen.getByLabelText(copy.discoveryBaseURL)).toHaveValue(address);
    expect(screen.getByLabelText(copy.discoveryBaseURL)).toHaveAccessibleDescription(
      copy.independentDiscovery,
    );
    await view.user.click(
      screen.getByRole('button', { name: user.user.services.saveListSettings }),
    );
    await waitFor(() => expect(saved).toHaveBeenCalledOnce());
    expect(writes).toEqual([
      { name: '', base_url: address, list_format: format, credential_id: '8', enabled: true },
    ]);
    expect(saved.mock.calls[0]![0].models_url).toBe(address);
  });
});
