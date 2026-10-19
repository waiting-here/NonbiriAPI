import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import type { HeaderSnapshot } from '@shared/observability/api';
import { HeaderFacts } from './HeaderFacts';

const requests = vi.hoisted(() => ({ getRequestHeaders: vi.fn(), getResponseHeaders: vi.fn() }));
vi.mock('@shared/observability/api', () => requests);
const id = 'req_AAAAAAAAAAAAAAAAAAAAAA';
const other = 'req_BBBBBBBBBBBBBBBBBBBBBQ';
const snapshot = (headers: HeaderSnapshot['headers'] = []): HeaderSnapshot => ({
  save_state: 'saved',
  captured_at: 1_800_000_000,
  http_status: 200,
  bytes_saved: 300,
  credentials_redacted: true,
  ipv4_redacted: false,
  headers,
});
const entry = (name: string, value: string) => ({
  name,
  value,
  name_encoding: 'utf-8' as const,
  value_encoding: 'utf-8' as const,
});

describe('complete log headers', () => {
  it('keeps the fold closed with zero reads, retries failures, and retains duplicate/empty/binary values as text', async () => {
    requests.getRequestHeaders
      .mockReset()
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce(
        snapshot([
          entry('X-Repeated', '<script>first</script>'),
          entry('X-Repeated', ''),
          { name: '/w==', name_encoding: 'base64', value: '/wD+', value_encoding: 'base64' },
        ]),
      );
    const view = await renderWithProviders(
      <HeaderFacts role="admin" accountID="one" requestID={id} />,
      { station: 'admin', locale: 'en' },
    );
    expect(requests.getRequestHeaders).not.toHaveBeenCalled();
    await view.user.click(screen.getByText('Complete request headers'));
    await view.user.click(await screen.findByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('<script>first</script>')).toBeInTheDocument();
    expect(screen.getAllByText('X-Repeated')).toHaveLength(2);
    expect(screen.getByText('(empty value)')).toBeInTheDocument();
    expect(screen.getByText('/wD+')).toBeInTheDocument();
    expect(screen.getAllByText('Base64')).toHaveLength(2);
    expect(view.container.querySelector('script')).toBeNull();
    expect(requests.getRequestHeaders).toHaveBeenCalledTimes(2);
    await view.user.click(screen.getByText('Complete request headers'));
    await view.user.click(screen.getByText('Complete request headers'));
    expect(requests.getRequestHeaders).toHaveBeenCalledTimes(2);
  });

  it('searches complete values across physical responses with bounded pages and level 5 privacy copy', async () => {
    const long = 'x'.repeat(80_000) + ' full-tail';
    requests.getResponseHeaders.mockReset().mockResolvedValue({
      save_state: 'saved',
      responses: [
        {
          ...snapshot(
            Array.from({ length: 125 }, (_, i) => entry('X-' + i, i === 124 ? long : 'value-' + i)),
          ),
          response_index: 1,
          ipv4_redacted: true,
        },
        {
          ...snapshot([entry('Second', 'response two')]),
          response_index: 2,
          http_status: 503,
          captured_at: 1_800_000_002,
        },
      ],
    });
    const view = await renderWithProviders(
      <HeaderFacts role="steward" accountID="five" requestID={id} attempt={2} />,
      { station: 'user', locale: 'en' },
    );
    expect(requests.getResponseHeaders).not.toHaveBeenCalled();
    await view.user.click(screen.getByText('Complete response headers'));
    expect(await screen.findByRole('heading', { name: 'Response 2' })).toBeInTheDocument();
    expect(screen.getByText('HTTP 503')).toBeInTheDocument();
    expect(screen.getByText(/separate observed IP/)).toBeInTheDocument();
    expect(within(screen.getByRole('table')).getAllByRole('row')).toHaveLength(21);
    fireEvent.change(screen.getByRole('textbox', { name: 'Go to page' }), {
      target: { value: '7' },
    });
    await view.user.click(screen.getByRole('button', { name: 'Go' }));
    expect(screen.getByText(long)).toBeInTheDocument();
    await view.user.selectOptions(screen.getByRole('combobox', { name: 'Items per page' }), '100');
    expect(within(screen.getByRole('table')).getAllByRole('row')).toHaveLength(101);
    fireEvent.change(screen.getByRole('searchbox'), { target: { value: 'full-tail' } });
    expect(screen.getByText(long)).toBeInTheDocument();
    expect(within(screen.getByRole('table')).getAllByRole('row')).toHaveLength(2);
    fireEvent.change(screen.getByRole('searchbox'), { target: { value: 'response two' } });
    expect(screen.getByText('response two')).toBeInTheDocument();
    expect(screen.getByText(/Credential values were replaced/)).toBeInTheDocument();
  });

  it.each(['not_recorded', 'capacity_exhausted', 'too_large', 'unavailable', 'saved'] as const)(
    'distinguishes %s from an empty capture',
    async (state) => {
      requests.getRequestHeaders
        .mockReset()
        .mockResolvedValue({ ...snapshot(), save_state: state });
      const view = await renderWithProviders(
        <HeaderFacts role="admin" accountID="one" requestID={id} />,
        { station: 'admin', locale: 'en' },
      );
      await view.user.click(screen.getByText('Complete request headers'));
      const messages = {
        not_recorded: /No headers were recorded/,
        capacity_exhausted: /storage was full/,
        too_large: /exceeded the storage limit/,
        unavailable: /currently unavailable/,
        saved: /Headers were captured; the list is empty/,
      };
      expect(await screen.findByText(messages[state])).toBeInTheDocument();
    },
  );

  it('resets on role, log and account changes, aborts late reads, and retains no inactive header cache', async () => {
    let resolve!: (value: HeaderSnapshot) => void;
    requests.getRequestHeaders
      .mockReset()
      .mockImplementationOnce(
        () =>
          new Promise<HeaderSnapshot>((done) => {
            resolve = done;
          }),
      )
      .mockResolvedValue(snapshot([entry('New', 'current account')]));
    const view = await renderWithProviders(
      <HeaderFacts role="admin" accountID="one" requestID={id} />,
      { station: 'admin', locale: 'en' },
    );
    await view.user.click(screen.getByText('Complete request headers'));
    await waitFor(() => expect(requests.getRequestHeaders).toHaveBeenCalledTimes(1));
    const signal = requests.getRequestHeaders.mock.calls[0][2] as AbortSignal;
    view.rerender(<HeaderFacts role="steward" accountID="two" requestID={other} />);
    expect(signal.aborted).toBe(true);
    await act(async () => {
      resolve(snapshot([entry('Old', 'old private header')]));
    });
    expect(screen.queryByText('old private header')).toBeNull();
    expect(requests.getRequestHeaders).toHaveBeenCalledTimes(1);
    await view.user.click(screen.getByText('Complete request headers'));
    expect(await screen.findByText('current account')).toBeInTheDocument();
    expect(requests.getRequestHeaders.mock.calls[1].slice(0, 2)).toEqual(['steward', other]);
    view.unmount();
    await waitFor(() =>
      expect(
        view.queryClient
          .getQueryCache()
          .getAll()
          .filter((query) => query.queryKey.includes('headers')),
      ).toHaveLength(0),
    );
  });
});
