import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { IndependentDiagnostics } from './IndependentDiagnostics';
import {
  decodeIndependentDetail,
  decodeIndependentPage,
  syntheticImageMIME,
} from './independentApi';
const request = vi.hoisted(() => ({ apiFetch: vi.fn() }));
vi.mock('@shared/query/http', () => ({ apiFetch: request.apiFetch }));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ i18n: { resolvedLanguage: 'en' } }) }));
afterEach(() => request.apiFetch.mockReset());
function entry(id = '3', body = '<script>untrusted</script>') {
  return {
    id,
    kind: 'image_task',
    subject_id: 'img_AAAAAAAAAAAAAAAAAAAAAA',
    user_id: '17',
    attempt_seq: 3,
    event_seq: 1,
    http_status: 503,
    content_type: 'text/html',
    bytes_saved: new TextEncoder().encode(body).length,
    truncated: false,
    save_state: 'saved',
    created_at: 1800000000,
    expires_at: 1802592000,
    synthetic: false,
  };
}
function page(data = [entry()], next_before: string | null = null) {
  return { data, next_before, from: 1799999999, to: 1800000001 };
}
describe('independent management diagnostics', () => {
  it('loads bodies lazily, keeps external text inert and resets on account changes', async () => {
    const metadata = entry();
    request.apiFetch.mockResolvedValueOnce(page()).mockResolvedValueOnce({
      item: metadata,
      body: { ...metadata, encoding: 'utf-8', body: '<script>untrusted</script>' },
      source: { effective_ip: '192.0.2.17', ip_quality: 'direct_peer', user_agent: '<img src=x>' },
    });
    const view = render(<IndependentDiagnostics role="admin" accountId="a" scopeReady />);
    expect(request.apiFetch).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Search diagnostics' }));
    const summary = await screen.findByText(/img_AAAAA.*Attempt 3/);
    expect(request.apiFetch).toHaveBeenCalledTimes(1);
    fireEvent.click(summary);
    fireEvent.click(screen.getByRole('button', { name: 'Load error details and source' }));
    expect(await screen.findByText('<script>untrusted</script>')).toBeInTheDocument();
    expect(screen.getByText('<img src=x>')).toBeInTheDocument();
    expect(document.querySelector('script')).toBeNull();
    expect(document.querySelector('img')).toBeNull();
    view.rerender(<IndependentDiagnostics role="admin" accountId="b" scopeReady />);
    expect(screen.queryByText('<script>untrusted</script>')).toBeNull();
    expect(screen.queryByText('<img src=x>')).toBeNull();
    expect(request.apiFetch).toHaveBeenCalledTimes(2);
    view.rerender(<IndependentDiagnostics role="admin" accountId="b" scopeReady={false} />);
    expect(screen.queryByRole('button', { name: 'Search diagnostics' })).toBeNull();
  });
  it('labels omitted, truncated and synthetic details without calling a summary original', async () => {
    const text =
      '{"category":"image_response_omitted","reason":"invalid_response","original_body_saved":false}';
    const synthetic = { ...entry('3', text), content_type: syntheticImageMIME, synthetic: true };
    const omitted = { ...entry('2'), save_state: 'capacity_exhausted', bytes_saved: 0 };
    const truncated = { ...entry('1'), truncated: true };
    request.apiFetch
      .mockResolvedValueOnce(page([synthetic, omitted, truncated]))
      .mockResolvedValueOnce({
        item: synthetic,
        body: { ...synthetic, encoding: 'utf-8', body: text },
        source: null,
      });
    render(<IndependentDiagnostics role="steward" accountId={6} scopeReady />);
    fireEvent.click(screen.getByRole('button', { name: 'Search diagnostics' }));
    expect(await screen.findByText(/storage budget was full/)).toBeInTheDocument();
    expect(screen.getByText(/truncated$/)).toBeInTheDocument();
    fireEvent.click(screen.getByText(/safe diagnostic$/));
    fireEvent.click(screen.getByRole('button', { name: 'Load safe diagnostic and source' }));
    expect(await screen.findByText(/not the original upstream body/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Download diagnostic summary' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Download original' })).toBeNull();
    expect(String(request.apiFetch.mock.calls[0][0])).toContain('/api/steward/diagnostics?');
  });
  it('keeps applied filters and replaces each bounded page', async () => {
    request.apiFetch
      .mockResolvedValueOnce(page([entry('3')], '3'))
      .mockResolvedValueOnce(page([entry('2')]));
    render(<IndependentDiagnostics role="admin" accountId="a" scopeReady />);
    fireEvent.change(screen.getByLabelText('Category'), { target: { value: 'image_task' } });
    fireEvent.change(screen.getByLabelText('User ID'), { target: { value: '17' } });
    fireEvent.click(screen.getByRole('button', { name: 'Search diagnostics' }));
    await screen.findByRole('button', { name: 'Next diagnostics' });
    fireEvent.change(screen.getByLabelText('User ID'), { target: { value: '99' } });
    fireEvent.click(screen.getByRole('button', { name: 'Next diagnostics' }));
    await waitFor(() => expect(request.apiFetch).toHaveBeenCalledTimes(2));
    const target = new URL(String(request.apiFetch.mock.calls[1][0]), 'https://example.invalid');
    expect(target.searchParams.get('user_id')).toBe('17');
    expect(target.searchParams.get('before')).toBe('3');
    expect(screen.getAllByText(/img_AAAAA.*Attempt 3/)).toHaveLength(1);
  });
  it('removes previously loaded sensitive text after a rejected list refresh', async () => {
    const metadata = entry();
    request.apiFetch
      .mockResolvedValueOnce(page())
      .mockResolvedValueOnce({
        item: metadata,
        body: { ...metadata, encoding: 'utf-8', body: '<script>untrusted</script>' },
        source: null,
      })
      .mockRejectedValueOnce(new Error('Access changed'));
    render(<IndependentDiagnostics role="admin" accountId="a" scopeReady />);
    fireEvent.click(screen.getByRole('button', { name: 'Search diagnostics' }));
    fireEvent.click(await screen.findByText(/img_AAAAA.*Attempt 3/));
    fireEvent.click(screen.getByRole('button', { name: 'Load error details and source' }));
    await screen.findByText('<script>untrusted</script>');
    fireEvent.click(screen.getByRole('button', { name: 'Search diagnostics' }));
    await screen.findByRole('alert');
    expect(screen.queryByText('<script>untrusted</script>')).toBeNull();
  });
  it('rejects oversized pages, invalid cursor ordering, foreign roots and corrupt body counts', () => {
    expect(() => decodeIndependentPage(page(Array.from({ length: 21 }, () => entry())))).toThrow();
    expect(() => decodeIndependentPage(page([entry('2'), entry('3')]))).toThrow();
    expect(() =>
      decodeIndependentPage(page([{ ...entry(), subject_id: 'op_AAAAAAAAAAAAAAAAAAAAAA' }])),
    ).toThrow();
    expect(() => decodeIndependentPage(page([entry()], '9223372036854775808'))).toThrow();
    const metadata = entry();
    expect(() =>
      decodeIndependentDetail({
        item: metadata,
        body: { ...metadata, encoding: 'utf-8', body: 'short' },
        source: null,
      }),
    ).toThrow();
    expect(() =>
      decodeIndependentDetail({
        item: metadata,
        body: { ...metadata, encoding: 'utf-8', body: '<script>untrusted</script>' },
        source: { effective_ip: '', ip_quality: 'direct_peer', authorization: 'forbidden' },
      }),
    ).toThrow();
  });
});
