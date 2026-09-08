import { useState } from 'react';
import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { TimeInput } from '../../src/shared/components/TimeInput';
import {
  createTimeDraft,
  timeDraftValue,
  type ResolvedTime,
  type TimeStation,
} from '../../src/shared/time';
import { renderWithProviders } from './support';

let browserZone = 'America/New_York';
const nativeOptions = Intl.DateTimeFormat.prototype.resolvedOptions;
const registry = { version: 'go1.26.6-zoneinfo', zones: ['America/New_York', 'Asia/Tokyo', 'UTC'] };
const gap: ResolvedTime = {
  instant: 1772955000,
  local: '2026-03-08T03:30:00',
  time_zone: 'America/New_York',
  offset_seconds: -14400,
  adjustment: 'gap_shifted',
};
const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
function Harness({
  epoch = null,
  station = 'user',
}: {
  epoch?: number | null;
  station?: TimeStation;
}) {
  const [draft, setDraft] = useState(() => createTimeDraft(epoch));
  const [note, setNote] = useState('');
  const [saved, setSaved] = useState<number | null | undefined>(undefined);
  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        const value = timeDraftValue(draft);
        if (value !== undefined) setSaved(value);
      }}
    >
      <TimeInput label="End time" station={station} draft={draft} onChange={setDraft} />
      <label>
        Note
        <input value={note} onChange={(event) => setNote(event.target.value)} />
      </label>
      <button disabled={timeDraftValue(draft) === undefined}>Save</button>
      <output aria-label="Saved">{saved === undefined ? 'unsaved' : String(saved)}</output>
    </form>
  );
}

beforeEach(() => {
  browserZone = 'America/New_York';
  vi.spyOn(Intl.DateTimeFormat.prototype, 'resolvedOptions').mockImplementation(function (
    this: Intl.DateTimeFormat,
  ) {
    return { ...nativeOptions.call(this), timeZone: browserZone };
  });
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('wall-clock input', () => {
  it.each(['user', 'admin'] as const)(
    'shows the adjustment before a %s save and keeps unrelated edits',
    async (station) => {
      let complete: ((response: Response) => void) | undefined;
      const fetchMock = vi.fn((path: string) =>
        path.endsWith('/time-zones')
          ? Promise.resolve(json(registry))
          : new Promise<Response>((resolve) => {
              complete = resolve;
            }),
      );
      vi.stubGlobal('fetch', fetchMock);
      const view = await renderWithProviders(<Harness station={station} />, { station });
      fireEvent.change(screen.getByLabelText('End time'), {
        target: { value: '2026-03-08T02:30' },
      });
      expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
      await waitFor(() => expect(complete).toBeTypeOf('function'));
      await view.user.type(screen.getByLabelText('Note'), 'keep this');
      await act(async () => {
        complete!(json(gap));
      });
      expect(screen.getByText(/will be saved as 2026-03-08 03:30:00/)).toBeVisible();
      expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled();
      await view.user.click(screen.getByRole('button', { name: 'Save' }));
      expect(screen.getByLabelText('Saved')).toHaveTextContent(String(gap.instant));
      expect(screen.getByLabelText('Note')).toHaveValue('keep this');
      const base = station === 'admin' ? '/admin/api' : '/api';
      expect(fetchMock.mock.calls.map(([path]) => path)).toContain(
        `${base}/time/resolve?local=2026-03-08T02%3A30%3A00&time_zone=America%2FNew_York`,
      );
    },
  );

  it('discards a late result after text is restored and preserves the original earlier fold and seconds', async () => {
    let complete: ((response: Response) => void) | undefined;
    let signal: AbortSignal | undefined;
    vi.stubGlobal(
      'fetch',
      vi.fn((path: string, options: RequestInit) =>
        path.endsWith('/time-zones')
          ? Promise.resolve(json(registry))
          : new Promise<Response>((resolve) => {
              complete = resolve;
              signal = options.signal as AbortSignal;
            }),
      ),
    );
    const epoch = Date.parse('2026-11-01T05:30:47Z') / 1000;
    const view = await renderWithProviders(<Harness epoch={epoch} />, { station: 'user' });
    fireEvent.change(screen.getByLabelText('End time'), { target: { value: '2026-03-08T02:30' } });
    await waitFor(() => expect(complete).toBeTypeOf('function'));
    fireEvent.change(screen.getByLabelText('End time'), { target: { value: '2026-11-01T01:30' } });
    expect(signal?.aborted).toBe(true);
    await act(async () => {
      complete!(json(gap));
    });
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    expect(screen.getByLabelText('Saved')).toHaveTextContent(String(epoch));
    expect(screen.queryByText(/will be saved as/)).not.toBeInTheDocument();
  });

  it('keeps a failed input, prevents submit, then retries explicitly', async () => {
    let fail = true;
    vi.stubGlobal(
      'fetch',
      vi.fn((path: string) =>
        Promise.resolve(
          path.endsWith('/time-zones')
            ? json(registry)
            : fail
              ? json({ error: { code: 'invalid_request', message: 'invalid request' } }, 400)
              : json(gap),
        ),
      ),
    );
    const view = await renderWithProviders(<Harness />, { station: 'user' });
    fireEvent.change(screen.getByLabelText('End time'), { target: { value: '2026-03-08T02:30' } });
    expect(await screen.findByText(/Your input has been kept/)).toBeVisible();
    expect(screen.getByLabelText('End time')).toHaveValue('2026-03-08T02:30');
    expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
    fireEvent.submit(view.container.querySelector('form')!);
    expect(screen.getByLabelText('Saved')).toHaveTextContent('unsaved');
    fail = false;
    await view.user.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled());
  });

  it('re-displays unchanged instants on a browser zone change and invalidates edited results', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn((path: string) =>
        Promise.resolve(path.endsWith('/time-zones') ? json(registry) : json(gap)),
      ),
    );
    const epoch = Date.parse('2026-11-01T05:30:47Z') / 1000;
    const view = await renderWithProviders(<Harness epoch={epoch} />, { station: 'user' });
    await waitFor(() => expect(screen.getByText('Time zone: America/New_York')).toBeVisible());
    browserZone = 'Asia/Tokyo';
    fireEvent.focus(window);
    await waitFor(() => expect(screen.getByLabelText('End time')).toHaveValue('2026-11-01T14:30'));
    expect(screen.getByText(/browser time zone changed/)).toBeVisible();
    await view.user.click(screen.getByRole('button', { name: 'Save' }));
    expect(screen.getByLabelText('Saved')).toHaveTextContent(String(epoch));
  });

  it.each(['', 'Unsupported/Zone'])(
    'requires visible UTC confirmation for unavailable zone %s',
    async (zone) => {
      browserZone = zone;
      const utc: ResolvedTime = {
        instant: 0,
        local: '1970-01-01T00:00:00',
        time_zone: 'UTC',
        offset_seconds: 0,
        adjustment: 'none',
      };
      const fetchMock = vi.fn((path: string) =>
        Promise.resolve(path.endsWith('/time-zones') ? json(registry) : json(utc)),
      );
      vi.stubGlobal('fetch', fetchMock);
      const view = await renderWithProviders(<Harness />, { station: 'user' });
      fireEvent.change(screen.getByLabelText('End time'), {
        target: { value: '1970-01-01T00:00' },
      });
      expect(await screen.findByRole('button', { name: 'Use UTC' })).toBeVisible();
      expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
      expect(fetchMock.mock.calls.every(([path]) => path.endsWith('/time-zones'))).toBe(true);
      await view.user.click(screen.getByRole('button', { name: 'Use UTC' }));
      await waitFor(() => expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled());
      await view.user.click(screen.getByRole('button', { name: 'Save' }));
      expect(screen.getByLabelText('Saved')).toHaveTextContent('0');
    },
  );
});
