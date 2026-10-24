import { useMemo, useState } from 'react';
import { act, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { TimeContextProvider } from '../TimeContext';
import { timeContextQueryKey } from '@shared/time';
import { PriceConfigFields } from './PriceConfigFields';
import { draftPriceConfig, priceDraft, priceDraftError } from './priceDraft';
import { renderWithProviders } from '../../../../test/unit/support';

const json = (body: unknown) =>
  new Response(JSON.stringify(body), { headers: { 'Content-Type': 'application/json' } });

function PriceEditor() {
  const [draft, setDraft] = useState(priceDraft);
  return (
    <>
      <PriceConfigFields
        value={draft}
        role="steward"
        onChange={(fields) => setDraft((current) => ({ ...current, ...fields }))}
      />
      <output aria-label="Validation">{priceDraftError(draft) ?? 'valid'}</output>
      <output aria-label="Discount">{JSON.stringify(draftPriceConfig(draft)?.discount)}</output>
    </>
  );
}

function ContextHost() {
  const [revision, setRevision] = useState(0);
  // Keep the editor mounted while its outer browser context renders again.
  const editor = useMemo(
    () => (
      <TimeContextProvider station="steward">
        <PriceEditor />
      </TimeContextProvider>
    ),
    [],
  );
  return (
    <>
      <button onClick={() => setRevision((value) => value + 1)}>Render context {revision}</button>
      <TimeContextProvider station="user">{editor}</TimeContextProvider>
    </>
  );
}

afterEach(() => vi.unstubAllGlobals());

it.each(['context', 'station'] as const)(
  'keeps nullable charity dates valid when the %s cache refreshes after a browser context render',
  async (scope) => {
    const fetchMock = vi.fn((path: string) =>
      Promise.resolve(
        json(
          path === '/api/steward/time-context'
            ? { mode: 'site', offset_minutes: 480 }
            : { mode: 'browser', offset_minutes: null },
        ),
      ),
    );
    vi.stubGlobal('fetch', fetchMock);
    const view = await renderWithProviders(<ContextHost />, { station: 'user' });
    const discount = JSON.stringify({ enabled: false, percent: 0, start_at: null, end_at: null });
    await waitFor(() => expect(screen.getByLabelText('Discount')).toHaveTextContent(discount));
    await view.user.click(screen.getByRole('button', { name: 'Render context 0' }));
    await act(async () =>
      view.queryClient.invalidateQueries({
        queryKey: scope === 'station' ? ['user'] : timeContextQueryKey('steward'),
      }),
    );
    expect(fetchMock.mock.calls.map(([path]) => path)).toEqual([
      '/api/steward/time-context',
      '/api/steward/time-context',
    ]);
    await waitFor(() => expect(screen.getByLabelText('Validation')).toHaveTextContent('valid'));
    expect(screen.getByLabelText('Discount')).toHaveTextContent(discount);
    for (const input of view.container.querySelectorAll('input[type="datetime-local"]'))
      expect(input).toBeEnabled();
  },
);
