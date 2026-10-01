import {
  beginManagementSessionRequest,
  noteManagementSessionSuccess,
} from '@shared/charityManagement';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { CustomPresets } from './CustomPresets';
import { customPresetList } from './presetApi';
import { initialSelection } from './selection';
import { testCatalog } from './testCatalog';

vi.mock('../common/duel/copy', async () => {
  const actual = await vi.importActual<typeof import('../common/duel/copy')>('../common/duel/copy');
  const { testDuelText } = await import('../common/duel/copy.test-support');
  return { ...actual, useDuelText: () => testDuelText(actual.duelCopyKeys, 'en') };
});

const selection = initialSelection(testCatalog.modes.quick);
const stored = (slot: number, revision = '1', mode: 'quick' | 'standard' = 'quick') => ({
  slot,
  name: '',
  revision,
  mode,
  loadout: initialSelection(testCatalog.modes[mode]),
  updated_at: 100,
});
const response = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });

function mount(fetchMock: ReturnType<typeof vi.fn>, blocked = false, onLoad = vi.fn()) {
  vi.stubGlobal('fetch', fetchMock);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(['user', 'session'], {
    user: { id: '1', username: 'player', effective_level: 1 },
  });
  const view = render(
    <QueryClientProvider client={client}>
      <CustomPresets
        catalogs={testCatalog.modes}
        mode="quick"
        selection={selection}
        blocked={blocked}
        onLoad={onLoad}
      />
    </QueryClientProvider>,
  );
  return { ...view, onLoad, client };
}

afterEach(() => vi.unstubAllGlobals());

describe('account custom presets', () => {
  it('shows all ten slots and saves without joining a queue', async () => {
    let slots: ReturnType<typeof stored>[] = [];
    const fetchMock = vi.fn(async (path: string, options: RequestInit) => {
      if (options.method === 'PUT') {
        expect(path).toBe('/api/games/likes/loadouts/1');
        expect(new Headers(options.headers).get('Idempotency-Key')).toMatch(
          /^[A-Za-z0-9_-]{22,128}$/,
        );
        const body = JSON.parse(options.body as string);
        expect(body).toEqual({ expected_revision: '0', mode: 'quick', loadout: selection });
        slots = [stored(1)];
        return response(slots[0]);
      }
      return response({ capacity: 10, slots });
    });
    mount(fetchMock);
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Save to Preset1' })).toBeEnabled(),
    );
    expect(screen.getAllByText('Empty slot')).toHaveLength(10);
    fireEvent.click(screen.getByRole('button', { name: 'Save to Preset1' }));
    await screen.findByText('Preset saved.');
    expect(screen.getByRole('button', { name: 'Load Preset1' })).toBeEnabled();
    expect(fetchMock.mock.calls.filter(([, options]) => options.method === 'PUT')).toHaveLength(1);
  });

  it('loads a saved mode and confirms overwriting its revision', async () => {
    let slots = [stored(3, '7', 'standard')];
    const fetchMock = vi.fn(async (_path: string, options: RequestInit) => {
      if (options.method === 'PUT') {
        const body = JSON.parse(options.body as string);
        expect(body.expected_revision).toBe('7');
        expect(body.mode).toBe('quick');
        slots = [stored(3, '8', 'quick')];
        return response(slots[0]);
      }
      return response({ capacity: 10, slots });
    });
    const onLoad = vi.fn();
    mount(fetchMock, false, onLoad);
    await waitFor(() => expect(screen.getByRole('button', { name: 'Load Preset3' })).toBeEnabled());
    fireEvent.click(screen.getByRole('button', { name: 'Load Preset3' }));
    expect(onLoad).toHaveBeenCalledWith('standard', stored(3, '7', 'standard').loadout);
    fireEvent.click(screen.getByRole('button', { name: 'Overwrite Preset3' }));
    expect(
      screen.getByRole('alertdialog', { name: 'Overwrite custom presets' }),
    ).toBeInTheDocument();
    expect(fetchMock.mock.calls.filter(([, options]) => options.method === 'PUT')).toHaveLength(0);
    fireEvent.click(screen.getByRole('button', { name: 'Confirm overwrite' }));
    await screen.findByText('Preset saved.');
    expect(fetchMock.mock.calls.filter(([, options]) => options.method === 'PUT')).toHaveLength(1);
  });

  it('refreshes a stale slot and preserves its selection after a conflict', async () => {
    let slots = [stored(2, '2')];
    const revisions: string[] = [];
    const fetchMock = vi.fn(async (_path: string, options: RequestInit) => {
      if (options.method === 'PUT') {
        const body = JSON.parse(options.body as string);
        revisions.push(body.expected_revision);
        if (revisions.length === 1) {
          slots = [stored(2, '3')];
          return response({ error: { code: 'conflict', message: 'state conflict' } }, 409);
        }
        slots = [stored(2, '4')];
        return response(slots[0]);
      }
      return response({ capacity: 10, slots });
    });
    const onLoad = vi.fn();
    mount(fetchMock, false, onLoad);
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Overwrite Preset2' })).toBeEnabled(),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Overwrite Preset2' }));
    fireEvent.click(screen.getByRole('button', { name: 'Confirm overwrite' }));
    await screen.findByText(/Custom presets changed elsewhere/);
    await waitFor(() =>
      expect(fetchMock.mock.calls.filter(([, options]) => options.method === 'GET')).toHaveLength(
        2,
      ),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Overwrite Preset2' }));
    fireEvent.click(screen.getByRole('button', { name: 'Confirm overwrite' }));
    await screen.findByText('Preset saved.');
    expect(revisions).toEqual(['2', '3']);
    expect(onLoad).not.toHaveBeenCalled();
  });

  it('keeps controls disabled while queued or playing and leaves failed loads nonblocking', async () => {
    const fetchMock = vi.fn(async (...args: [string, RequestInit]) => {
      expect(args).toHaveLength(2);
      return response({ capacity: 10, slots: [stored(1)] });
    });
    const { rerender, unmount, client } = mount(fetchMock, true);
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Overwrite Preset1' })).toBeDisabled(),
    );
    rerender(
      <QueryClientProvider client={client}>
        <CustomPresets
          catalogs={testCatalog.modes}
          mode="quick"
          selection={selection}
          blocked
          onLoad={vi.fn()}
        />
      </QueryClientProvider>,
    );
    expect(screen.getByRole('button', { name: 'Load Preset1' })).toBeDisabled();
    expect(fetchMock.mock.calls.filter(([, options]) => options.method === 'PUT')).toHaveLength(0);
    unmount();

    const failing = vi.fn(async () => {
      throw new Error('offline');
    });
    mount(failing);
    await screen.findByText(/Custom presets could not be loaded/);
    expect(screen.getByRole('button', { name: 'Save to Preset1' })).toBeDisabled();
  });

  it('does not show cached private slots before a fresh account read', async () => {
    let complete!: (value: Response) => void;
    const pending = new Promise<Response>((resolve) => {
      complete = resolve;
    });
    const fetchMock = vi.fn(async () => pending);
    vi.stubGlobal('fetch', fetchMock);
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    client.setQueryData(['user', 'session'], {
      user: { id: '1', username: 'player', effective_level: 1 },
    });
    client.setQueryData(['user', 'games', 'likes', 'custom-presets', '1'], {
      capacity: 10,
      slots: [stored(1)],
    });
    render(
      <QueryClientProvider client={client}>
        <CustomPresets
          catalogs={testCatalog.modes}
          mode="quick"
          selection={selection}
          blocked={false}
          onLoad={vi.fn()}
        />
      </QueryClientProvider>,
    );
    expect(screen.getByRole('button', { name: 'Save to Preset1' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Load Preset1' })).toBeDisabled();
    complete(response({ capacity: 10, slots: [stored(2)] }));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Load Preset2' })).toBeEnabled());
    expect(screen.getByRole('button', { name: 'Save to Preset1' })).toBeEnabled();
  });

  it('rejects malformed or out-of-order private slot data', () => {
    expect(() => customPresetList({ capacity: 10, slots: [stored(2), stored(1)] })).toThrow();
    expect(() =>
      customPresetList({
        capacity: 10,
        slots: [
          { ...stored(1), loadout: { role: 'ChatGPT', harness: null, skills: ['PUB01', 'PUB01'] } },
        ],
      }),
    ).toThrow();
  });
});

describe('preset names and summaries', () => {
  it('shows the complete stored order and repairs unavailable IDs only through an explicit overwrite', async () => {
    const item = stored(1);
    item.loadout = {
      ...selection,
      harness: 'H01',
      skills: [selection.skills[1], selection.skills[0], 'gone-skill'],
    };
    const fetchMock = vi.fn(async (_path: string, options: RequestInit) => {
      if (options.method === 'PUT') {
        expect(JSON.parse(options.body as string).loadout).toEqual(selection);
        return response(stored(1, '2'));
      }
      return response({ capacity: 10, slots: [item] });
    });
    const { onLoad } = mount(fetchMock);
    const card = await screen.findByRole('article', { name: 'Preset1' });
    await within(card).findByText('Unavailable: gone-skill');
    expect(
      within(card).getByText(testCatalog.modes.quick.harnesses.find((h) => h.id === 'H01')!.name),
    ).toBeInTheDocument();
    expect(
      within(card)
        .getAllByRole('listitem')
        .map((node) => node.textContent),
    ).toEqual([
      ...item.loadout.skills
        .slice(0, 2)
        .map((id) => testCatalog.modes.quick.skills.find((skill) => skill.id === id)!.name),
      'Unavailable: gone-skill',
    ]);
    expect(within(card).getByRole('button', { name: 'Load Preset1' })).toBeDisabled();
    expect(onLoad).not.toHaveBeenCalled();
    fireEvent.click(within(card).getByRole('button', { name: 'Overwrite Preset1' }));
    fireEvent.click(screen.getByRole('button', { name: 'Confirm overwrite' }));
    await screen.findByText('Preset saved.');
    expect(onLoad).not.toHaveBeenCalled();
  });

  it('saves and clears a Unicode short name without loading or changing the stored selection', async () => {
    let item = { ...stored(1), name: 'Old name' };
    const original = structuredClone(item.loadout);
    const fetchMock = vi.fn(async (_path: string, options: RequestInit) => {
      if (options.method === 'PATCH') {
        const body = JSON.parse(options.body as string);
        expect(Object.keys(body).sort()).toEqual(['expected_revision', 'name']);
        item = { ...item, name: body.name, revision: String(Number(item.revision) + 1) };
        return response(item);
      }
      return response({ capacity: 10, slots: [item] });
    });
    const { onLoad } = mount(fetchMock);
    const input = await screen.findByRole('textbox', { name: 'Preset1 name' });
    fireEvent.change(input, { target: { value: '鱼'.repeat(19) + '🐟' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    await screen.findByText('Preset saved.');
    expect(fetchMock.mock.calls.filter(([, options]) => options.method === 'PATCH')).toHaveLength(
      1,
    );
    expect(item.loadout).toEqual(original);
    expect(onLoad).not.toHaveBeenCalled();
    await waitFor(() => expect(input).toBeEnabled());
    fireEvent.change(input, { target: { value: 'Unsaved name' } });
    expect(screen.queryByText('Preset saved.')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Clear name' }));
    await waitFor(() => expect(item.name).toBe(''));
    await waitFor(() => expect(input).toHaveValue(''));
    expect(item.loadout).toEqual(original);
    expect(onLoad).not.toHaveBeenCalled();
  });

  it('retains the original rename identity while checking and retrying an unknown response', async () => {
    let item = stored(1);
    const requests: { key: string | null; body: unknown }[] = [];
    const fetchMock = vi.fn(async (_path: string, options: RequestInit) => {
      if (options.method === 'PATCH') {
        requests.push({
          key: new Headers(options.headers).get('Idempotency-Key'),
          body: JSON.parse(options.body as string),
        });
        if (requests.length === 1) throw new Error('response lost');
        item = { ...item, revision: '2', name: 'Fast' };
        return response(item);
      }
      return response({ capacity: 10, slots: [item] });
    });
    mount(fetchMock);
    fireEvent.change(await screen.findByRole('textbox', { name: 'Preset1 name' }), {
      target: { value: 'Fast' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save name' }));
    await screen.findByText(
      'Saving could not be confirmed. Check current presets or retry saving.',
    );
    expect(screen.getByRole('textbox', { name: 'Preset1 name' })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: 'Check current presets' }));
    await waitFor(() =>
      expect(
        fetchMock.mock.calls.filter(([, options]) => options.method === 'GET').length,
      ).toBeGreaterThanOrEqual(3),
    );
    fireEvent.click(screen.getByRole('button', { name: 'Retry saving custom presets' }));
    await screen.findByText('Preset saved.');
    expect(requests).toHaveLength(2);
    expect(requests[1]).toEqual(requests[0]);
  });

  it('closes the old confirmation and ignores a late rename after an account switch', async () => {
    let account = '1';
    let finish!: (value: Response) => void;
    const oldRequest = new Promise<Response>((resolve) => {
      finish = resolve;
    });
    const fetchMock = vi.fn(async (_path: string, options: RequestInit) => {
      if (options.method === 'PATCH') return oldRequest;
      return response({
        capacity: 10,
        slots: [{ ...stored(1), name: account === '1' ? 'Old account' : 'New account' }],
      });
    });
    const { client } = mount(fetchMock);
    fireEvent.change(await screen.findByRole('textbox', { name: 'Preset1 name' }), {
      target: { value: 'Late name' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save name' }));
    await waitFor(() =>
      expect(fetchMock.mock.calls.some(([, options]) => options.method === 'PATCH')).toBe(true),
    );
    account = '2';
    act(() => {
      const value = { user: { id: '2', username: 'other-player', effective_level: 1 } };
      const generation = beginManagementSessionRequest(client, 'steward');
      expect(noteManagementSessionSuccess(client, 'steward', value, generation)).toBe(true);
      client.setQueryData(['user', 'session'], value);
    });
    await screen.findByText('New account');
    await act(async () => finish(response({ ...stored(1, '2'), name: 'Late name' })));
    expect(screen.getByRole('textbox', { name: 'Preset1 name' })).toHaveValue('New account');
    expect(screen.queryByText('Preset saved.')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Overwrite Preset1' }));
    expect(screen.getByRole('alertdialog')).toBeInTheDocument();
    act(() => {
      const value = { user: { id: '3', username: 'third-player', effective_level: 1 } };
      const generation = beginManagementSessionRequest(client, 'steward');
      expect(noteManagementSessionSuccess(client, 'steward', value, generation)).toBe(true);
      client.setQueryData(['user', 'session'], value);
    });
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument());
  });
});
