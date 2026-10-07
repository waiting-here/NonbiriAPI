import { StrictMode } from 'react';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router';
import { beforeEach, expect, it, vi } from 'vitest';
import { SteadyCatchGame } from './SteadyCatchGame';
import { newGame } from './engine';
import type { Controls, Session } from './session';

const calls = vi.hoisted(() => ({ request: vi.fn(), coarse: false }));
vi.mock('./copy', () => ({ useCatchText: () => (zh: string) => zh }));
vi.mock('./Board', () => ({ CatchBoard: () => <canvas /> }));
vi.mock('./Leaderboard', () => ({ CatchLeaderboard: () => null }));
vi.mock('../common/GameWallets', () => ({ GameWallets: () => null }));
vi.mock('../common/GameBackLink', () => ({
  GameBackLink: () => <a href="/games">返回游戏中心</a>,
}));
vi.mock('../common/request', () => ({
  createIdempotencyKey: () => 'fixture-key',
  gameRequest: calls.request,
}));
vi.mock('../common/snapshot', () => ({
  gameKeys: { snapshot: ['snapshot'] },
  useGamesSnapshot: () => ({
    data: {
      gamesEnabled: true,
      steadycatch: {
        enabled: true,
        available: true,
        price: '2',
        firstClearReward: '3',
        firstCleared: false,
      },
    },
  }),
}));
class TestImage {
  static instances: TestImage[] = [];
  onload: (() => void) | null = null;
  onerror: (() => void) | null = null;
  src = '';
  constructor() {
    TestImage.instances.push(this);
  }
}
let authority: Session | null;
let releasePause: (() => void) | undefined;
let holdPause = false;
let controls: Controls['action'][];
beforeEach(() => {
  authority = null;
  holdPause = false;
  releasePause = undefined;
  controls = [];
  TestImage.instances = [];
  calls.coarse = false;
  vi.stubGlobal('Image', TestImage);
  vi.stubGlobal('matchMedia', () => ({ matches: calls.coarse }));
  vi.spyOn(HTMLDialogElement.prototype, 'showModal').mockImplementation(function (
    this: HTMLDialogElement,
  ) {
    this.open = true;
  });
  vi.spyOn(HTMLDialogElement.prototype, 'close').mockImplementation(function (
    this: HTMLDialogElement,
  ) {
    if (!this.open) return;
    this.open = false;
    this.dispatchEvent(new Event('close'));
  });
  calls.request.mockImplementation(async (path: string, options?: { json: Controls }) => {
    if (path.endsWith('/catalog'))
      return { data: [{ id: 'white', text: '稳稳地接住你', category: 'test', gold: false }] };
    if (path.includes('/leaderboard')) return { data: { rows: [], me: null } };
    if (path.endsWith('/session')) return { data: authority };
    if (path.endsWith('/sessions')) {
      authority = {
        id: 'sc_fixture',
        status: 'paused',
        revision: 1,
        state: newGame(1),
        payment: { general: '2', game: '0' },
        first_clear_reward: '3',
        first_clear: false,
        reward: '0',
        created_at: 1,
        expires_at: 1801,
        terminal_at: null,
        server_ms: 1000,
      };
      return { data: authority };
    }
    if (path.endsWith('/controls')) {
      const action = options!.json.action;
      controls.push(action);
      if (action === 'pause' && holdPause)
        await new Promise<void>((resolve) => {
          releasePause = resolve;
        });
      authority = {
        ...authority!,
        revision: authority!.revision + 1,
        status: action === 'pause' ? 'paused' : 'playing',
      };
      return { data: authority };
    }
    throw new Error('Unexpected fixture route: ' + path);
  });
});
function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <StrictMode>
      <QueryClientProvider client={client}>
        <MemoryRouter>
          <SteadyCatchGame />
        </MemoryRouter>
      </QueryClientProvider>
    </StrictMode>,
  );
}
async function ready() {
  await act(async () => {
    TestImage.instances.at(-1)!.onload?.();
  });
  await screen.findByRole('button', { name: '开始接住' });
}
it('keeps entry unpaid until the character loads and explains a failed load', async () => {
  mount();
  const button = await screen.findByRole('button', { name: '接物机准备中…' });
  expect(button).toBeDisabled();
  fireEvent.click(button);
  await act(async () => {
    TestImage.instances.at(-1)!.onerror?.();
  });
  expect(screen.getByRole('button', { name: '角色未能载入' })).toBeDisabled();
  expect(screen.getByRole('alert')).toHaveTextContent('请刷新页面后再试');
  expect(calls.request.mock.calls.some(([path]) => path.endsWith('/sessions'))).toBe(false);
});
it('uses the original touch hints and catalog heading on coarse pointers', async () => {
  calls.coarse = true;
  mount();
  await ready();
  expect(screen.getByText('按住场内左右滑动 · 或长按方向按钮')).toBeVisible();
  expect(screen.getByText('按住场内，左右滑动')).toBeVisible();
  fireEvent.click(screen.getByRole('button', { name: /梗图鉴/ }));
  expect(screen.getByText('接物机的语料仓库')).toBeVisible();
  expect(
    screen.getByRole('heading', { name: '八股梗图鉴 1' }).querySelector('span'),
  ).toHaveTextContent('1');
});
it('starts once under StrictMode and waits for the saved pause before closing a menu resumes play', async () => {
  const view = mount();
  await ready();
  fireEvent.click(screen.getByRole('button', { name: '开始接住' }));
  await waitFor(() => expect(screen.getByRole('button', { name: '暂停游戏' })).toBeEnabled());
  expect(controls).toEqual(['resume']);
  holdPause = true;
  fireEvent.click(screen.getByRole('button', { name: /梗图鉴/ }));
  await waitFor(() => expect(controls).toEqual(['resume', 'pause']));
  fireEvent.click(screen.getByRole('dialog', { name: '八股梗图鉴' }).querySelector('button')!);
  expect(controls).toEqual(['resume', 'pause']);
  holdPause = false;
  await act(async () => {
    releasePause!();
  });
  await waitFor(() => expect(controls).toEqual(['resume', 'pause', 'resume']));
  expect(screen.getByRole('button', { name: '暂停游戏' })).toBeEnabled();
  view.unmount();
  await waitFor(() => expect(controls).toEqual(['resume', 'pause', 'resume', 'pause']));
});
