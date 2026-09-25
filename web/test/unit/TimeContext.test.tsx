import { useState } from 'react';
import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { TimeContextNotice, TimeContextProvider } from '../../src/shared/components/TimeContext';
import { useDateTimeFormatter } from '../../src/shared/utils/datetime';
import { renderWithProviders } from './support';

const instant = Date.parse('2030-01-01T00:00:00Z') / 1000;
const json = (value: unknown) =>
  new Response(JSON.stringify(value), { headers: { 'Content-Type': 'application/json' } });

function Probe({ name }: { name: string }) {
  const formatDateTime = useDateTimeFormatter();
  const [note, setNote] = useState('');
  return (
    <div>
      <output aria-label={name}>{formatDateTime(instant, 'en')}</output>
      <input
        aria-label={`${name} note`}
        value={note}
        onChange={(event) => setNote(event.target.value)}
      />
    </div>
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe('station display time context', () => {
  it('fails closed until the site offset arrives and updates existing displays without remounting a form', async () => {
    let complete!: (response: Response) => void;
    vi.stubGlobal(
      'fetch',
      vi.fn(
        () =>
          new Promise<Response>((resolve) => {
            complete = resolve;
          }),
      ),
    );
    const view = await renderWithProviders(
      <TimeContextProvider station="admin">
        <Probe name="admin time" />
      </TimeContextProvider>,
      { station: 'admin' },
    );
    expect(screen.getByLabelText('admin time')).toHaveTextContent('—');
    await waitFor(() => expect(complete).toBeTypeOf('function'));
    fireEvent.change(screen.getByLabelText('admin time note'), {
      target: { value: 'unsaved work' },
    });
    await act(async () => complete(json({ mode: 'site', offset_minutes: 330 })));
    await waitFor(() => expect(screen.getByLabelText('admin time')).toHaveTextContent(/05:30/));
    expect(screen.getByLabelText('admin time note')).toHaveValue('unsaved work');

    act(() =>
      view.queryClient.setQueryData(['admin', 'time-context'], { mode: 'site', offset_minutes: 0 }),
    );
    await waitFor(() => expect(screen.getByLabelText('admin time')).toHaveTextContent(/12:00/));
    expect(screen.getByLabelText('admin time note')).toHaveValue('unsaved work');
    act(() =>
      view.queryClient.setQueryData(['admin', 'time-context'], {
        mode: 'site',
        offset_minutes: null,
      }),
    );
    await waitFor(() => expect(screen.getByLabelText('admin time')).toHaveTextContent('—'));
  });

  it('keeps nested steward time separate from browser time when switching and unmounting', async () => {
    const original = Intl.DateTimeFormat.prototype.resolvedOptions;
    vi.spyOn(Intl.DateTimeFormat.prototype, 'resolvedOptions').mockImplementation(function (
      this: Intl.DateTimeFormat,
    ) {
      return { ...original.call(this), timeZone: 'America/New_York' };
    });
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => json({ mode: 'site', offset_minutes: 330 })),
    );
    const userTree = (steward: boolean) => (
      <TimeContextProvider station="user">
        <Probe name="user time" />
        {steward ? (
          <TimeContextProvider station="steward">
            <Probe name="steward time" />
          </TimeContextProvider>
        ) : null}
      </TimeContextProvider>
    );
    const view = await renderWithProviders(userTree(true), { station: 'user' });
    expect(screen.getByLabelText('user time')).toHaveTextContent(/07:00/);
    expect(screen.getByLabelText('steward time')).toHaveTextContent('—');
    await waitFor(() => expect(screen.getByLabelText('steward time')).toHaveTextContent(/05:30/));
    expect(screen.getByLabelText('user time')).toHaveTextContent(/07:00/);
    view.rerender(userTree(false));
    expect(screen.queryByLabelText('steward time')).not.toBeInTheDocument();
    expect(screen.getByLabelText('user time')).toHaveTextContent(/07:00/);
  });

  it('shows one site notice and warns only when the browser offset differs', async () => {
    const original = Intl.DateTimeFormat.prototype.resolvedOptions;
    vi.spyOn(Intl.DateTimeFormat.prototype, 'resolvedOptions').mockImplementation(function (
      this: Intl.DateTimeFormat,
    ) {
      return { ...original.call(this), timeZone: 'UTC' };
    });
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => json({ mode: 'site', offset_minutes: 0 })),
    );
    const view = await renderWithProviders(
      <TimeContextProvider station="admin">
        <TimeContextNotice station="admin" />
      </TimeContextProvider>,
      { station: 'admin' },
    );
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('UTC+00:00'));
    expect(screen.getByRole('status')).not.toHaveTextContent(/browser/i);
    act(() =>
      view.queryClient.setQueryData(['admin', 'time-context'], {
        mode: 'site',
        offset_minutes: 330,
      }),
    );
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent(/browser/i));
    view.rerender(<TimeContextNotice station="user" />);
    expect(screen.getByRole('status')).toHaveTextContent(/Local time/);
    expect(screen.getByRole('status')).not.toHaveTextContent(/browser/i);
  });
});
