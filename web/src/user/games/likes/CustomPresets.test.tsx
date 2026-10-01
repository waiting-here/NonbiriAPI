import { afterEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { CustomPresets } from './CustomPresets';
import { customPresetList } from './presetApi';
import { initialSelection } from './selection';
import { testCatalog } from './testCatalog';

vi.mock('../common/duel/copy', () => ({ useDuelText: () => (_zh: string, en: string) => en }));

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
    await screen.findByText('Custom presets: Preset1 saved.');
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
    await screen.findByText('Custom presets: Preset3 overwritten.');
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
    await screen.findByText('Custom presets: Preset2 overwritten.');
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
    client.setQueryData(['user', 'games', 'likes', 'custom-presets'], {
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
