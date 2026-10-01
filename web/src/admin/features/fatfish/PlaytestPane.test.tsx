import { act, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { ApiError } from '@shared/query/http';
import { renderWithProviders } from '../../../../test/unit/support';
import { adminPlaytestTransport, PlaytestPane } from './PlaytestPane';

const http = vi.hoisted(() => ({ apiFetch: vi.fn() }));
const sessions = vi.hoisted(() => ({ create: vi.fn() }));
vi.mock('@shared/query/http', async (importOriginal) => ({
  ...await importOriginal<typeof import('@shared/query/http')>(), apiFetch: http.apiFetch,
}));
vi.mock('@shared/fatfish/session', () => ({ createFatFishSessionController: sessions.create }));
vi.mock('@shared/fatfish/storage', () => ({ localPlaySupportError: () => null }));
vi.mock('@shared/fatfish/FatFishPlayer', () => ({ FatFishPlayer: () => <div>Shared playtest player</div> }));
const hash = 'a'.repeat(64);
const prepared = { id: 'ffc_one', revision: '1', version_id: 'ffv_one', content_hash: hash, ticket_price: '0', state: 'prepared' };
const metadata = { id: 'ffc_old', revision: '2', version_id: 'ffv_old', version_number: '3', level_title: 'Old level', content_hash: hash, state: 'active' };
function controller(initial = prepared) {
  let snapshot = { phase: initial.state === 'prepared' ? 'prepared' : 'running', challenge: initial };
  const listeners = new Set<() => void>();
  return {
    snapshot: () => snapshot,
    subscribe: (listener: () => void) => { listeners.add(listener); return () => listeners.delete(listener); },
    prepare: vi.fn(async () => snapshot.challenge),
    start: vi.fn(async () => {
      snapshot = { phase: 'running', challenge: { ...snapshot.challenge, state: 'active' } };
      listeners.forEach((listener) => listener()); return snapshot.challenge;
    }),
    recover: vi.fn(async () => snapshot.challenge), dispose: vi.fn(),
  };
}
beforeEach(() => { http.apiFetch.mockReset().mockResolvedValue(null); sessions.create.mockReset(); sessionStorage.clear(); });

it('uses the admin-only abandon revision and enables durable automatic submission', async () => {
  const transport = adminPlaytestTransport('ffv_one');
  expect(transport.prepareScope).toBe('playtest:ffv_one');
  expect(transport.autoSubmit).toBe(true);
  await transport.prepare('b'.repeat(64), 'prepare-key');
  await transport.start('ffc_one', 'tab-secret', 'start-key');
  await transport.read('ffc_one', 'tab-secret');
  await transport.abandon!('ffc_one', 'tab-secret', 'abandon-key', '4');
  expect(http.apiFetch.mock.calls.map((call) => call[0])).toEqual([
    '/admin/api/limited-activities/fat-fish/playtests',
    '/admin/api/limited-activities/fat-fish/playtests/ffc_one/start',
    '/admin/api/limited-activities/fat-fish/playtests/ffc_one',
    '/admin/api/limited-activities/fat-fish/playtests/ffc_one/abandon',
  ]);
  expect(http.apiFetch.mock.calls[3][1].json).toEqual({ expected_revision: '4' });
  expect((http.apiFetch.mock.calls[3][1].headers as Headers).get('Idempotency-Key')).toBe('abandon-key');
});
it('prepares only on request and starts manually', async () => {
  const candidate = controller(); sessions.create.mockReturnValue(candidate);
  const view = await renderWithProviders(<PlaytestPane versionID="ffv_one" contentHash={hash} />, { station: 'admin', role: 'admin' });
  await act(async () => { view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } }); });
  expect(candidate.prepare).not.toHaveBeenCalled();
  await view.user.click(screen.getByRole('button', { name: 'Prepare playtest' }));
  await waitFor(() => expect(candidate.prepare).toHaveBeenCalledTimes(1));
  expect(candidate.start).not.toHaveBeenCalled();
  await view.user.click(await screen.findByRole('button', { name: 'Start playtest' }));
  await waitFor(() => expect(candidate.start).toHaveBeenCalledTimes(1));
});
it('recovers the one unfinished playtest and exposes a lost-capability abandon choice', async () => {
  http.apiFetch.mockResolvedValue(metadata);
  const candidate = controller(); candidate.recover.mockRejectedValue(new Error('missing capability'));
  sessions.create.mockReturnValue(candidate);
  const view = await renderWithProviders(<PlaytestPane />, { station: 'admin', role: 'admin' });
  await act(async () => { view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } }); });
  await waitFor(() => expect(candidate.recover).toHaveBeenCalledWith('ffc_old'));
  expect(screen.getByText(/Old level.*Version 3.*active/)).toBeInTheDocument();
  expect(candidate.prepare).not.toHaveBeenCalled();
  expect(screen.queryByRole('button', { name: 'Start playtest' })).not.toBeInTheDocument();
  await view.user.click(screen.getByRole('button', { name: 'Abandon playtest' }));
  expect(await screen.findByRole('alertdialog')).toBeInTheDocument();
});
it('switches once after explicit abandon and retains a lost abandon reply key', async () => {
  const old = controller({ ...prepared, ...metadata, ticket_price: '0' }), next = controller();
  sessions.create.mockReturnValueOnce(old).mockReturnValueOnce(next);
  let abandonAttempts = 0;
  http.apiFetch.mockImplementation(async (path: string) => {
    if (path.endsWith('/current')) return metadata;
    if (path.endsWith('/abandon')) {
      if (++abandonAttempts === 1) throw new ApiError('unavailable', 'Reply lost', 502);
      return { ...metadata, state: 'abandoned' };
    }
    throw new Error('Unexpected route');
  });
  const view = await renderWithProviders(<PlaytestPane versionID="ffv_one" contentHash={hash} />, { station: 'admin', role: 'admin' });
  await act(async () => { view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } }); });
  await waitFor(() => expect(old.recover).toHaveBeenCalledWith('ffc_old'));
  await view.user.click(screen.getByRole('button', { name: 'Prepare playtest' }));
  const dialog = await screen.findByRole('alertdialog');
  expect(next.prepare).not.toHaveBeenCalled();
  await view.user.click(within(dialog).getByRole('button', { name: 'Abandon playtest' }));
  await screen.findByText('Reply lost');
  expect(dialog).toBeInTheDocument();
  await view.user.click(within(dialog).getByRole('button', { name: 'Abandon playtest' }));
  await waitFor(() => expect(next.prepare).toHaveBeenCalledTimes(1));
  const calls = http.apiFetch.mock.calls.filter((call) => call[0].endsWith('/abandon'));
  expect(calls[1][1].json).toEqual(calls[0][1].json);
  expect((calls[1][1].headers as Headers).get('Idempotency-Key')).toBe((calls[0][1].headers as Headers).get('Idempotency-Key'));
  expect(old.dispose).toHaveBeenCalled();
  expect(next.start).not.toHaveBeenCalled();
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
});
it('disposes a late prepared response after the administrator session changes', async () => {
  const candidate = controller();
  let release: (value: typeof prepared) => void = () => {};
  candidate.prepare.mockImplementation(() => new Promise((resolve) => { release = resolve; }));
  sessions.create.mockReturnValue(candidate);
  const view = await renderWithProviders(<PlaytestPane versionID="ffv_one" contentHash={hash} />, { station: 'admin', role: 'admin' });
  await act(async () => { view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } }); });
  await view.user.click(screen.getByRole('button', { name: 'Prepare playtest' }));
  await waitFor(() => expect(candidate.prepare).toHaveBeenCalledTimes(1));
  await act(async () => {
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'replacement-admin' } });
    release(prepared);
  });
  await waitFor(() => expect(candidate.dispose).toHaveBeenCalled());
  expect(screen.queryByRole('button', { name: 'Start playtest' })).not.toBeInTheDocument();
});
it('disposes an in-flight initial recovery after the administrator session changes', async () => {
  http.apiFetch.mockResolvedValueOnce(metadata).mockResolvedValue(null);
  const candidate = controller({ ...prepared, ...metadata, ticket_price: '0' });
  let release: (value: typeof prepared) => void = () => {};
  candidate.recover.mockImplementation(() => new Promise((resolve) => { release = resolve; }));
  sessions.create.mockReturnValue(candidate);
  const view = await renderWithProviders(<PlaytestPane />, { station: 'admin', role: 'admin' });
  await act(async () => { view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } }); });
  await waitFor(() => expect(candidate.recover).toHaveBeenCalledTimes(1));
  await act(async () => {
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'replacement-admin' } });
    release({ ...prepared, ...metadata, ticket_price: '0' });
  });
  await waitFor(() => expect(candidate.dispose).toHaveBeenCalled());
  expect(screen.queryByText('Shared playtest player')).not.toBeInTheDocument();
});

it('does not reopen an abandon dialog from an old account read', async () => {
  http.apiFetch.mockResolvedValueOnce(metadata).mockResolvedValue(null);
  const candidate = controller(); candidate.recover.mockRejectedValue(new Error('missing capability'));
  sessions.create.mockReturnValue(candidate);
  const view = await renderWithProviders(<PlaytestPane />, { station: 'admin', role: 'admin' });
  await act(async () => { view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'fixture-admin' } }); });
  await waitFor(() => expect(candidate.recover).toHaveBeenCalledTimes(1));
  let release: (value: typeof metadata) => void = () => {};
  http.apiFetch.mockImplementationOnce(() => new Promise((resolve) => { release = resolve; }));
  await view.user.click(screen.getByRole('button', { name: 'Abandon playtest' }));
  await act(async () => {
    view.queryClient.setQueryData(['admin', 'session'], { admin: { username: 'replacement-admin' } });
    release(metadata);
  });
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument();
  expect(screen.queryByText(/Old level/)).not.toBeInTheDocument();
});
