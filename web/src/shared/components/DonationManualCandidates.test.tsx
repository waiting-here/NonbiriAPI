import { screen, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import { CharityModelScope } from './CharityModelScope';
import { DonationManualCandidates } from './DonationManualCandidates';

function response(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { 'content-type': 'application/json' },
  });
}
const candidate = {
  upstream_model_id: 'model-one',
  display_name: 'model-one',
  source: 'manual',
  verified: false,
  manual_entry_id: '31',
  manual_entry_revision: '1',
};
function page() {
  const metadata = { page: '1', page_size: 20, total_items: '1', total_pages: '1' };
  return {
    data: [],
    next_cursor: null,
    pagination: { ...metadata, total_items: '0' },
    candidates: [candidate],
    candidates_pagination: metadata,
    manual_catalog_revision: '5',
  };
}

describe('manual donation candidates', () => {
  it('retries an unconfirmed original request with the same scope, names and operation key', async () => {
    const writes: { body: unknown; key: string | null; url: URL }[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(async (input, init) => {
        const url = new URL(String(input), 'https://example.test');
        expect(url.searchParams.get('charity_model_id')).toBe('21');
        if ((init?.method ?? 'GET') === 'GET') return response(page());
        writes.push({
          body: JSON.parse(String(init?.body)),
          key: new Headers(init?.headers).get('Idempotency-Key'),
          url,
        });
        if (writes.length === 1)
          return response({ error: { code: 'server_error', message: 'Temporary failure' } }, 500);
        return response({ entries: [candidate], manual_catalog_revision: '6' });
      }),
    );
    const view = await renderWithProviders(
      <CharityModelScope modelID="21">
        <DonationManualCandidates role="steward" accountId="7" donationId="11" keyId="9" />
      </CharityModelScope>,
      { station: 'user', role: 'level5' },
    );
    view.queryClient.setQueryData(['user', 'session'], {
      user: { id: '7', username: 'fixture-steward', effective_level: 5 },
    });
    await screen.findByText('model-one');
    await view.user.type(
      screen.getByLabelText('Add model names manually'),
      'model-two\nmodel-three',
    );
    await view.user.click(screen.getByRole('button', { name: 'Add candidates' }));
    await screen.findByRole('button', { name: 'Retry the original action' });
    expect(screen.getByLabelText('Add model names manually')).toBeDisabled();
    await view.user.click(screen.getByRole('button', { name: 'Retry the original action' }));
    await waitFor(() => expect(writes).toHaveLength(2));
    expect(writes[0].body).toEqual({
      entries: ['model-two', 'model-three'],
      expected_manual_catalog_revision: '5',
    });
    expect(writes[1].body).toEqual(writes[0].body);
    expect(writes[1].key).toBe(writes[0].key);
    expect(writes[0].url.pathname).toBe('/api/steward/donations/11/keys/9/models/manual');
    expect(await screen.findByRole('status')).toHaveTextContent('Candidate changes saved.');
    await view.user.type(screen.getByLabelText('Add model names manually'), 'another-model');
    expect(screen.queryByText('Candidate changes saved.')).not.toBeInTheDocument();
    expect(writes).toHaveLength(2);
  });

  it('keeps terminal candidates readable without exposing metadata writes', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockImplementation(() => response(page())),
    );
    await renderWithProviders(
      <DonationManualCandidates
        role="admin"
        accountId="fixture-admin"
        donationId="11"
        keyId="9"
        editable={false}
      />,
      { station: 'admin', role: 'admin' },
    );
    expect(await screen.findByText('model-one')).toBeVisible();
    expect(screen.queryByRole('button', { name: 'Add candidates' })).not.toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: /Remove manual candidate/ }),
    ).not.toBeInTheDocument();
  });
});
