import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { DiscoveryFailureDiagnostics } from './DiscoveryFailureDiagnostics';

const requests = vi.hoisted(() => ({
  list: vi.fn(),
  detail: vi.fn(),
}));
vi.mock('@shared/observability/independentApi', () => ({
  getIndependentDiagnostics: requests.list,
  getIndependentDiagnostic: requests.detail,
}));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ i18n: { resolvedLanguage: 'en' } }) }));

const firstOperation = 'op_AAAAAAAAAAAAAAAAAAAAAA';
const secondOperation = 'op_BBBBBBBBBBBBBBBBBBBBBB';
function item(id: string, operationID: string) {
  return {
    id,
    kind: 'image_discovery',
    subject_id: operationID,
    user_id: '17',
    attempt_seq: 1,
    event_seq: 1,
    http_status: 503,
    content_type: 'text/plain',
    bytes_saved: 7,
    truncated: false,
    save_state: 'saved',
    created_at: 1800000000,
    expires_at: 1802592000,
    synthetic: false,
  };
}
function page(data: ReturnType<typeof item>[]) {
  return { data, next_before: null, from: 1799999999, to: 1800000001 };
}
function detail(entry: ReturnType<typeof item>, body = 'private') {
  return { item: entry, body: { ...entry, encoding: 'utf-8', body }, source: null };
}
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

afterEach(() => {
  requests.list.mockReset();
  requests.detail.mockReset();
});

describe('failed image discovery diagnostics', () => {
  it('aborts an old operation request and ignores its late result', async () => {
    const old = deferred<ReturnType<typeof page>>();
    requests.list
      .mockReturnValueOnce(old.promise)
      .mockResolvedValueOnce(page([{ ...item('2', secondOperation), attempt_seq: 2 }]));
    const view = render(<DiscoveryFailureDiagnostics operationID={firstOperation} />);
    fireEvent.click(screen.getByRole('button', { name: 'Load retained diagnostics' }));
    const oldSignal = requests.list.mock.calls[0][2] as AbortSignal;
    view.rerender(<DiscoveryFailureDiagnostics operationID={secondOperation} />);
    expect(oldSignal.aborted).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: 'Load retained diagnostics' }));
    await screen.findByText(/Attempt 2 \/ event 1/);
    expect(requests.list.mock.calls[1][1]).toEqual({
      kind: 'image_discovery',
      subject_id: secondOperation,
    });
    await act(async () => old.resolve(page([item('3', firstOperation)])));
    expect(screen.getAllByText(/Attempt 2 \/ event 1/)).toHaveLength(1);
    expect(screen.queryByText(/Attempt 1 \/ event 1/)).toBeNull();
    view.unmount();
  });

  it('clears loaded raw text after a failed refresh or detail read', async () => {
    const first = item('3', firstOperation);
    const second = item('2', firstOperation);
    requests.list
      .mockResolvedValueOnce(page([first, second]))
      .mockRejectedValueOnce(new Error('denied'));
    requests.detail.mockResolvedValueOnce(detail(first)).mockRejectedValueOnce(new Error('denied'));
    render(<DiscoveryFailureDiagnostics operationID={firstOperation} />);
    fireEvent.click(screen.getByRole('button', { name: 'Load retained diagnostics' }));
    await waitFor(() => expect(screen.getAllByText(/Attempt 1 \/ event 1/)).toHaveLength(2));
    fireEvent.click(screen.getAllByText(/Attempt 1 \/ event 1/)[0]);
    fireEvent.click(screen.getAllByRole('button', { name: 'Load retained response body' })[0]);
    expect(await screen.findByText('private')).toBeInTheDocument();
    fireEvent.click(screen.getAllByText(/Attempt 1 \/ event 1/)[1]);
    fireEvent.click(screen.getByRole('button', { name: 'Load retained response body' }));
    await screen.findByRole('alert');
    expect(screen.queryByText('private')).toBeNull();
    expect(screen.queryByText(/Attempt 1 \/ event 1/)).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Load retained diagnostics' }));
    await waitFor(() => expect(requests.list).toHaveBeenCalledTimes(2));
    await screen.findByRole('alert');
    expect(screen.queryByText('private')).toBeNull();
  });

  it('cancels a pending body read when the operation changes', async () => {
    const pending = deferred<ReturnType<typeof detail>>();
    requests.list.mockResolvedValue(page([item('3', firstOperation)]));
    requests.detail.mockReturnValue(pending.promise);
    const view = render(<DiscoveryFailureDiagnostics operationID={firstOperation} />);
    fireEvent.click(screen.getByRole('button', { name: 'Load retained diagnostics' }));
    fireEvent.click(await screen.findByText(/Attempt 1 \/ event 1/));
    fireEvent.click(screen.getByRole('button', { name: 'Load retained response body' }));
    const signal = requests.detail.mock.calls[0][2] as AbortSignal;
    view.rerender(<DiscoveryFailureDiagnostics operationID={secondOperation} />);
    expect(signal.aborted).toBe(true);
    await act(async () => pending.resolve(detail(item('3', firstOperation))));
    expect(screen.queryByText('private')).toBeNull();
  });
});
