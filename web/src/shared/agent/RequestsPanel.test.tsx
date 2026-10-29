import { normalizeFailure } from '@shared/observability/failure';
import { screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../test/unit/support';
import type { AgentRequest } from './api';
import { RequestsPanel, UsageTokens } from './RequestsPanel';
const mocks = vi.hoisted(() => ({ get: vi.fn(), detail: vi.fn(), cleanup: vi.fn() }));
vi.mock('./api', async (original) => ({
  ...(await original<typeof import('./api')>()),
  getRequests: mocks.get,
  getRequest: mocks.detail,
  cleanupRequests: mocks.cleanup,
}));
const usage = {
  input_tokens: '9007199254740993',
  cached_input_tokens: '0',
  cache_write_input_tokens: '0',
  output_tokens: '0',
  reasoning_tokens: '0',
  unknown: true,
};
const request = {
  id: '9007199254740993',
  actor_id: '5',
  actor_label: 'Synthetic operator',
  source: 'steward_chat',
  kind: 'model',
  state: 'succeeded',
  created_at: 1700000000000,
  usage,
} as AgentRequest;
describe('administrator Agent request history', () => {
  it('freezes the confirmation cutoff and never includes newly arriving requests', async () => {
    const page = {
      items: [request],
      total: '1',
      page: 1,
      page_size: 20 as const,
      cutoff_id: '9007199254740997',
    };
    mocks.get.mockResolvedValue(page);
    mocks.cleanup.mockResolvedValue({ deleted_count: '1', skipped_count: '0' });
    const view = await renderWithProviders(<RequestsPanel account="synthetic" active />, {
      station: 'admin',
      role: 'admin',
    });
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'synthetic' } });
    await view.user.click(await screen.findByRole('button', { name: 'Clear matching history' }));
    const dialog = await screen.findByRole('alertdialog');
    expect(mocks.cleanup).not.toHaveBeenCalled();
    const current = view.queryClient
      .getQueryCache()
      .findAll({ queryKey: ['admin', 'ai', 'synthetic', 'requests'] })[0]!;
    view.queryClient.setQueryData(current.queryKey, {
      ...page,
      cutoff_id: '9007199254741000',
      total: '4',
    });
    await view.user.click(within(dialog).getByRole('button', { name: 'Clear history' }));
    await waitFor(() => expect(mocks.cleanup).toHaveBeenCalledTimes(1));
    expect(mocks.cleanup.mock.calls[0]![0].through_id).toBe('9007199254740997');
    expect(mocks.cleanup.mock.calls[0]![0]).not.toHaveProperty('ids');
  });
  it('uses exact selected string IDs without combining them with a range cutoff', async () => {
    mocks.get.mockResolvedValue({
      items: [request],
      total: '1',
      page: 1,
      page_size: 20,
      cutoff_id: '9007199254740997',
    });
    mocks.cleanup.mockResolvedValue({ deleted_count: '1', skipped_count: '0' });
    mocks.cleanup.mockClear();
    const view = await renderWithProviders(<RequestsPanel account="synthetic" active />, {
      station: 'admin',
      role: 'admin',
    });
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'synthetic' } });
    await view.user.click(
      await screen.findByRole('checkbox', { name: 'Select request 9007199254740993' }),
    );
    await view.user.click(screen.getByRole('button', { name: 'Clear selected (1)' }));
    const dialog = await screen.findByRole('alertdialog');
    await view.user.click(within(dialog).getByRole('button', { name: 'Clear history' }));
    await waitFor(() => expect(mocks.cleanup).toHaveBeenCalledTimes(1));
    expect(mocks.cleanup.mock.calls[0]![0].ids).toEqual(['9007199254740993']);
    expect(mocks.cleanup.mock.calls[0]![0]).not.toHaveProperty('through_id');
  });
  it('preserves large token strings and distinguishes unknown usage from zero', async () => {
    await renderWithProviders(<UsageTokens usage={usage} />, { station: 'admin', role: 'admin' });
    expect(screen.getByText('9,007,199,254,740,993')).toBeVisible();
    expect(screen.getAllByText('Unknown')).toHaveLength(4);
    expect(screen.getByText(/must not be treated as zero/)).toBeVisible();
  });
});

it('shows AI failure evidence only after opening request details', async () => {
  mocks.get.mockResolvedValue({ items: [request], total: '1', page: 1, page_size: 20 });
  mocks.detail.mockResolvedValue({
    ...request,
    state: 'failed',
    response_complete: false,
    queued_input: null,
    request_body: '',
    response_body: '',
    error_code: 'read_failed',
    error_diagnostics: normalizeFailure({
      category: 'network',
      phase: 'read_response',
      code: 'connection_reset',
      upstream_status: 200,
      protocol_complete: false,
      causes: [null],
    }),
  });
  const view = await renderWithProviders(<RequestsPanel account="synthetic" active />, {
    station: 'admin',
    role: 'admin',
  });
  expect(mocks.detail).not.toHaveBeenCalled();
  await view.user.click(await screen.findByRole('button', { name: 'Open' }));
  expect(await screen.findByText('The peer reset the connection')).toBeVisible();
  expect(screen.getByText('Completion was not confirmed')).toBeVisible();
  await view.user.click(screen.getByText('Technical evidence'));
  expect(screen.getByText(/Some diagnostic items were unreadable/)).toBeVisible();
  await view.user.click(screen.getByRole('button', { name: 'Back to list' }));
  expect(screen.queryByText('The peer reset the connection')).toBeNull();
});
