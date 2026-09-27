import { screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { adminPlaytestTransport, PlaytestPane } from './PlaytestPane';

const http = vi.hoisted(() => ({ apiFetch: vi.fn() }));
const sessions = vi.hoisted(() => ({ create: vi.fn() }));
vi.mock('@shared/query/http', async (importOriginal) => ({
  ...await importOriginal<typeof import('@shared/query/http')>(), apiFetch: http.apiFetch,
}));
vi.mock('@shared/fatfish/session', () => ({ createFatFishSessionController: sessions.create }));
vi.mock('@shared/fatfish/storage', () => ({ localPlaySupportError: () => null }));
vi.mock('@shared/fatfish/FatFishPlayer', () => ({ FatFishPlayer: ({ mode }: { mode: string }) => <div>Shared player: {mode}</div> }));

beforeEach(() => {
  http.apiFetch.mockReset().mockResolvedValue({ id: 'ffc_one' });
  sessions.create.mockReset();
  sessionStorage.clear();
});

it('uses the admin playtest routes and binds the prepare scope to one immutable version', async () => {
  const transport = adminPlaytestTransport('ffv_one');
  expect(transport.prepareScope).toBe('playtest:ffv_one');
  await transport.prepare('a'.repeat(64), 'prepare-key');
  await transport.start('ffc_one', 'tab-secret', 'start-key');
  await transport.read('ffc_one', 'tab-secret');
  await transport.submit('ffc_one', { tab_capability: 'tab-secret', inputs: [], terminal_tick: 12 }, 'submit-key');
  expect(http.apiFetch.mock.calls.map((call) => call[0])).toEqual([
    '/admin/api/limited-activities/fat-fish/playtests',
    '/admin/api/limited-activities/fat-fish/playtests/ffc_one/start',
    '/admin/api/limited-activities/fat-fish/playtests/ffc_one',
    '/admin/api/limited-activities/fat-fish/playtests/ffc_one/submit',
  ]);
  expect(http.apiFetch.mock.calls[0][1].json).toEqual({ version_id: 'ffv_one', tab_capability_hash: 'a'.repeat(64) });
  expect((http.apiFetch.mock.calls[0][1].headers as Headers).get('Idempotency-Key')).toBe('prepare-key');
  expect(http.apiFetch.mock.calls[1][1].json).toEqual({ tab_capability: 'tab-secret' });
  expect((http.apiFetch.mock.calls[1][1].headers as Headers).get('Idempotency-Key')).toBe('start-key');
  expect(http.apiFetch.mock.calls[2][1].headers).toEqual({ 'X-FatFish-Tab-Capability': 'tab-secret' });
  expect(http.apiFetch.mock.calls[3][1].json).toEqual({ tab_capability: 'tab-secret', inputs: [], terminal_tick: 12 });
  expect((http.apiFetch.mock.calls[3][1].headers as Headers).get('Idempotency-Key')).toBe('submit-key');
  expect(transport.current).toBeUndefined();
  expect(transport.abandon).toBeUndefined();
});

it('never prepares automatically and starts only an explicitly prepared no-charge playtest', async () => {
  const prepared = { id: 'ffc_one', version_id: 'ffv_one', content_hash: 'a'.repeat(64), ticket_price: '0', state: 'prepared' };
  let snapshot = { phase: 'idle', challenge: null as typeof prepared | null };
  const listeners = new Set<() => void>();
  const publish = () => { for (const listener of listeners) listener(); };
  const controller = {
    snapshot: () => snapshot,
    subscribe: (listener: () => void) => { listeners.add(listener); return () => listeners.delete(listener); },
    prepare: vi.fn(async () => { snapshot = { phase: 'prepared', challenge: prepared }; publish(); return prepared; }),
    start: vi.fn(async () => { snapshot = { phase: 'running', challenge: { ...prepared, state: 'active' } }; publish(); return snapshot.challenge; }),
    recover: vi.fn(), dispose: vi.fn(),
  };
  sessions.create.mockReturnValue(controller);
  const view = await renderWithProviders(<PlaytestPane versionID="ffv_one" contentHash={'a'.repeat(64)} />, { station: 'admin', role: 'admin' });
  expect(controller.prepare).not.toHaveBeenCalled();
  await view.user.click(screen.getByRole('button', { name: 'Prepare playtest' }));
  await waitFor(() => expect(controller.prepare).toHaveBeenCalledTimes(1));
  expect(sessionStorage.getItem('nonbiri-fatfish-admin-playtest:ffv_one')).toBe('ffc_one');
  expect(screen.getByText('Shared player: playtest')).toBeInTheDocument();
  await view.user.click(await screen.findByRole('button', { name: 'Start playtest' }));
  await waitFor(() => expect(controller.start).toHaveBeenCalledTimes(1));
  expect(sessions.create.mock.calls[0][0].prepareScope).toBe('playtest:ffv_one');
});

it('recovers only the remembered challenge for this version and keeps its original capability in the shared controller', async () => {
  sessionStorage.setItem('nonbiri-fatfish-admin-playtest:ffv_one', 'ffc_original');
  const prepared = { id: 'ffc_original', version_id: 'ffv_one', content_hash: 'a'.repeat(64), ticket_price: '0', state: 'prepared' };
  const controller = {
    snapshot: () => ({ phase: 'prepared', challenge: prepared }),
    subscribe: () => () => {}, recover: vi.fn(async () => prepared), prepare: vi.fn(), start: vi.fn(), dispose: vi.fn(),
  };
  sessions.create.mockReturnValue(controller);
  await renderWithProviders(<PlaytestPane versionID="ffv_one" contentHash={'a'.repeat(64)} />, { station: 'admin', role: 'admin' });
  await waitFor(() => expect(controller.recover).toHaveBeenCalledWith('ffc_original'));
  expect(controller.prepare).not.toHaveBeenCalled();
  expect(await screen.findByRole('button', { name: 'Start playtest' })).toBeInTheDocument();
});

it('refuses a remembered challenge whose immutable version hash changed', async () => {
  sessionStorage.setItem('nonbiri-fatfish-admin-playtest:ffv_one', 'ffc_original');
  const alien = { id: 'ffc_original', version_id: 'ffv_other', content_hash: 'b'.repeat(64), ticket_price: '0', state: 'prepared' };
  const controller = { snapshot: () => ({ phase: 'prepared', challenge: alien }), subscribe: () => () => {},
    recover: vi.fn(async () => alien), prepare: vi.fn(), start: vi.fn(), dispose: vi.fn() };
  sessions.create.mockReturnValue(controller);
  await renderWithProviders(<PlaytestPane versionID="ffv_one" contentHash={'a'.repeat(64)} />, { station: 'admin', role: 'admin' });
  await waitFor(() => expect(controller.dispose).toHaveBeenCalled());
  expect(screen.queryByRole('button', { name: 'Start playtest' })).not.toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Retry this tab’s playtest recovery' })).toBeInTheDocument();
  expect(controller.prepare).not.toHaveBeenCalled();
});

it('refuses to start when the admin preview unexpectedly reports a ticket price', async () => {
  const priced = { id: 'ffc_priced', version_id: 'ffv_one', content_hash: 'a'.repeat(64), ticket_price: '2', state: 'prepared' };
  const controller = { snapshot: () => ({ phase: 'prepared', challenge: priced }), subscribe: () => () => {},
    prepare: vi.fn(async () => priced), recover: vi.fn(), start: vi.fn(), dispose: vi.fn() };
  sessions.create.mockReturnValue(controller);
  const view = await renderWithProviders(<PlaytestPane versionID="ffv_one" contentHash={'a'.repeat(64)} />, { station: 'admin', role: 'admin' });
  await view.user.click(screen.getByRole('button', { name: 'Prepare playtest' }));
  await waitFor(() => expect(controller.dispose).toHaveBeenCalled());
  expect(controller.start).not.toHaveBeenCalled();
  expect(screen.queryByRole('button', { name: 'Start playtest' })).not.toBeInTheDocument();
  expect(sessionStorage.getItem('nonbiri-fatfish-admin-playtest:ffv_one')).toBe('ffc_priced');
});
