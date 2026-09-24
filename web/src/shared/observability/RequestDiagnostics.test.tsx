import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { AttemptErrors, RawErrorViewer } from './RequestDiagnostics';
import type { ErrorBody } from './api';

const requests = vi.hoisted(() => ({ apiFetch: vi.fn() }));
vi.mock('@shared/query/http', () => ({ apiFetch: requests.apiFetch }));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ i18n: { resolvedLanguage: 'en' } }) }));
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
    fireEvent.click(screen.getByRole('button', { name: 'Load original body' }));
    await waitFor(() => expect(requests.apiFetch).toHaveBeenCalledTimes(2));
    expect(await screen.findByText('<script>x</script>')).toBeInTheDocument();
    expect(document.querySelector('script')).toBeNull();
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
