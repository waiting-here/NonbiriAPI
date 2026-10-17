import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { AttemptErrors, RawErrorViewer } from './RequestDiagnostics';
import { errorBytes, type ErrorBody } from './api';

const requests = vi.hoisted(() => ({ apiFetch: vi.fn() }));
vi.mock('@shared/query/http', () => ({ apiFetch: requests.apiFetch }));
vi.mock('react-i18next', async () => {
  const { default: copy } = await import('@shared/i18n/common/en.json');
  return {
    useTranslation: () => ({
      t: (key: string) =>
        key
          .split('.')
          .reduce<unknown>((value, part) => (value as Record<string, unknown>)?.[part], copy) ??
        key,
    }),
  };
});
afterEach(() => {
  requests.apiFetch.mockReset();
});

describe('request diagnostics', () => {
  it('fetches metadata and original bodies only after explicit requests', async () => {
    const metadata = {
      event_seq: 1,
      http_status: 503,
      content_type: 'application/json',
      bytes_saved: 18,
      truncated: false,
      save_state: 'saved',
      created_at: 1,
      expires_at: 2,
    };
    requests.apiFetch
      .mockResolvedValueOnce({ data: [metadata], next_after: null })
      .mockResolvedValueOnce({ ...metadata, encoding: 'utf-8', body: '<script>x</script>' });
    render(<AttemptErrors role="admin" requestID="req_AAAAAAAAAAAAAAAAAAAAAA" attempt={1} />);
    expect(requests.apiFetch).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Upstream error details' }));
    await waitFor(() => expect(requests.apiFetch).toHaveBeenCalledTimes(1));
    fireEvent.click(await screen.findByText('Error event 1 · HTTP 503 · 18 B'));
    expect(requests.apiFetch).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole('button', { name: 'Load error body' }));
    await waitFor(() => expect(requests.apiFetch).toHaveBeenCalledTimes(2));
    expect(await screen.findByText('<script>x</script>')).toBeInTheDocument();
    expect(document.querySelector('script')).toBeNull();
  });
  it('renders the complete saved body beyond the old preview limit as text', () => {
    const text =
      'Failed at 192.0.2.7 person@example.invalid <script>x</script> ' +
      'x'.repeat(70_000) +
      ' saved-tail';
    const body = {
      event_seq: 1,
      encoding: 'utf-8',
      body: text,
      bytes_saved: new TextEncoder().encode(text).length,
      truncated: false,
    } as ErrorBody;
    const { container } = render(<RawErrorViewer body={body} />);
    fireEvent.click(screen.getByText('Body preview'));
    expect(container.querySelector('pre')?.textContent).toBe(text);
    expect(container.querySelector('script')).toBeNull();
    expect(screen.getByRole('button', { name: 'Download error body' })).toBeEnabled();
    expect(screen.queryByText(/replaced with \[redacted\]/)).toBeNull();
  });
  it('decodes the complete original binary bytes without text conversion', () => {
    const bytes = new Uint8Array([0xff, 0, ...new TextEncoder().encode('192.0.2.7 <script>')]);
    const body = { encoding: 'base64', body: btoa(String.fromCharCode(...bytes)) } as ErrorBody;
    expect(errorBytes(body)).toEqual(bytes);
  });
  it('keeps malformed body decoding failures inside the viewer', () => {
    render(<RawErrorViewer body={{ body: 'not base64!', encoding: 'base64' } as ErrorBody} />);
    expect(screen.getByRole('alert')).toHaveTextContent('Could not load the error body.');
    expect(screen.queryByRole('button', { name: 'Download error body' })).toBeNull();
  });
  it('does not label non UTF-8 or truncated bytes as formattable JSON', () => {
    const body = {
      event_seq: 2,
      encoding: 'base64',
      body: '/wD+',
      bytes_saved: 3,
      truncated: true,
    } as ErrorBody;
    render(<RawErrorViewer body={body} />);
    expect(screen.getByRole('button', { name: 'Format JSON' })).toBeDisabled();
    expect(screen.getByText(/Non-UTF-8 bytes/)).toBeInTheDocument();
    expect(screen.getByText(/truncated at 1 MiB/)).toBeInTheDocument();
  });
});
