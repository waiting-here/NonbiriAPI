import { act, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { GwentGame } from './GwentGame';
import type { DuelLobbyContext } from '../common/duel/types';

const mocks = vi.hoisted(() => ({
  run: vi.fn(),
  refresh: vi.fn(),
  historyRead: vi.fn(),
  retry: vi.fn(),
  current: null as null | { id: string; phaseSeq: string; decisionID?: string; deadline: number },
}));
vi.mock('../../data', () => ({
  useUserSession: () => ({ data: { user: { id: 'user-example' } } }),
}));
vi.mock('../common/duel/api', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../common/duel/api')>()),
  readHistory: mocks.historyRead,
  useDuel: () => ({
    query: { data: { serverNow: 0, current: mocks.current, queue: null, latestResult: null } },
    run: mocks.run,
    refresh: mocks.refresh,
    retry: mocks.retry,
    blocked: false,
    uncertain: false,
  }),
}));
const deck = {
  faction: 'openai',
  leader: 'openai_leader',
  cards: [{ id: 'openai_gpt4', count: 1 }],
};
const offer = {
  terms: {
    ticket: '0',
    ai: { bot_id: 'bot_AAAAAAAAAAAAAAAAAAAAAA', bot_name: 'Challenge', bot_loadout: deck },
  },
  terms_hash: 'a'.repeat(64),
  completed: false,
};
const context: DuelLobbyContext = {
  config: {
    enabled: true,
    available: true,
    modes: {
      standard: {
        enabled: true,
        available: true,
        ticket: '0',
        rates: { platform: 0, welfare: 0, thursday: 0 },
        termsHash: 'b'.repeat(64),
        contentHash: 'c'.repeat(64),
      },
    },
  },
  wallets: { balance: '0', gameBalance: '0' },
  accepting: true,
};
const dispatch = (
  frame: HTMLIFrameElement,
  data: Record<string, unknown>,
  origin = location.origin,
  source: MessageEventSource | null = frame.contentWindow,
) =>
  act(() =>
    window.dispatchEvent(
      new MessageEvent('message', { origin, source, data: { channel: 'nonbiri.gwent', ...data } }),
    ),
  );
async function mount() {
  vi.stubGlobal(
    'fetch',
    vi.fn(
      async () =>
        new Response(JSON.stringify({ enabled: true, bots: [offer] }), {
          headers: { 'Content-Type': 'application/json' },
        }),
    ),
  );
  const result = await renderWithProviders(<GwentGame {...context} />, { station: 'user' });
  const frame = result.container.querySelector('iframe')!;
  const send = vi.spyOn(frame.contentWindow!, 'postMessage');
  await waitFor(() => {
    dispatch(frame, { type: 'ready' });
    expect(send.mock.calls.some(([message]) => message.snapshot?.ai?.bots.length)).toBe(true);
  });
  return { result, frame, send };
}
describe('original arena platform bridge', () => {
  it('uses the displayed challenge terms and retains the queued deck on rematch', async () => {
    mocks.current = null;
    const { frame } = await mount();
    dispatch(frame, {
      type: 'queue',
      mode: 'ai',
      termsHash: offer.terms_hash,
      botID: offer.terms.ai.bot_id,
      deck,
    });
    expect(mocks.run).toHaveBeenLastCalledWith({
      kind: 'queue',
      mode: 'ai',
      termsHash: offer.terms_hash,
      botID: offer.terms.ai.bot_id,
      loadout: deck,
    });
    dispatch(frame, { type: 'rematch' });
    expect(mocks.run.mock.calls.at(-1)).toEqual(mocks.run.mock.calls.at(-2));
  });
  it('drops old or cancelled history responses after a newer choice', async () => {
    mocks.current = null;
    const { frame, send } = await mount();
    type Page = { items: never[]; nextCursor: string | null };
    let older!: (page: Page) => void, newer!: (page: Page) => void, closed!: (page: Page) => void;
    mocks.historyRead
      .mockImplementationOnce(
        () =>
          new Promise<Page>((resolve) => {
            older = resolve;
          }),
      )
      .mockImplementationOnce(
        () =>
          new Promise<Page>((resolve) => {
            newer = resolve;
          }),
      )
      .mockImplementationOnce(
        () =>
          new Promise<Page>((resolve) => {
            closed = resolve;
          }),
      );
    dispatch(frame, { type: 'history', cursor: 'old' });
    dispatch(frame, { type: 'history', cursor: 'new' });
    await act(async () => newer({ items: [], nextCursor: 'new-page' }));
    await act(async () => older({ items: [], nextCursor: 'old-page' }));
    let histories = send.mock.calls.filter(([message]) => message.type === 'history');
    expect(histories).toHaveLength(1);
    expect(histories[0]![0].page.nextCursor).toBe('new-page');
    dispatch(frame, { type: 'history' });
    dispatch(frame, { type: 'cancel-read' });
    await act(async () => closed({ items: [], nextCursor: 'closed-page' }));
    histories = send.mock.calls.filter(([message]) => message.type === 'history');
    expect(histories).toHaveLength(1);
  });
  it('keeps a loading return and reload control until the original frame mounts', async () => {
    mocks.current = null;
    const { result, frame } = await mount();
    expect(result.container.querySelector('.gwent-frame-fallback a')!.getAttribute('href')).toBe(
      '/games',
    );
    dispatch(frame, { type: 'failed' });
    expect(result.container.querySelector('.gwent-frame-fallback button')).not.toBeNull();
    dispatch(frame, { type: 'mounted' });
    expect(result.container.querySelector('.gwent-frame-fallback')).toBeNull();
  });
  it('requires the terms displayed in the iframe instead of rebinding a stale ticket', async () => {
    mocks.current = null;
    const { frame } = await mount();
    mocks.run.mockClear();
    dispatch(frame, {
      type: 'queue',
      mode: 'ai',
      termsHash: 'old-price',
      botID: offer.terms.ai.bot_id,
      deck,
    });
    expect(mocks.run).not.toHaveBeenCalled();
    expect(mocks.refresh).toHaveBeenCalled();
  });
  it('uses the independent decision for actions while surrender still requires the current global phase', async () => {
    mocks.current = {
      id: 'gwt_AAAAAAAAAAAAAAAAAAAAAA',
      phaseSeq: '4',
      decisionID: '9',
      deadline: 0,
    };
    const { frame, result } = await mount();
    mocks.current = { ...mocks.current, phaseSeq: '5' };
    result.rerender(<GwentGame {...context} />);
    mocks.run.mockClear();
    const message = {
      type: 'action',
      id: mocks.current.id,
      phaseSeq: '4',
      decisionID: '9',
      action: { kind: 'redraw', card: 2 },
    };
    dispatch(frame, message);
    expect(mocks.run).toHaveBeenCalledExactlyOnceWith({
      kind: 'action',
      id: message.id,
      phaseSeq: '5',
      decisionID: '9',
      action: message.action,
    });
    dispatch(frame, { ...message, phaseSeq: '5', decisionID: '8' });
    dispatch(frame, { type: 'surrender', id: message.id, phaseSeq: '4' });
    expect(mocks.run).toHaveBeenCalledTimes(1);
    dispatch(frame, { type: 'surrender', id: message.id, phaseSeq: '5' });
    expect(mocks.run).toHaveBeenLastCalledWith({
      kind: 'surrender',
      id: message.id,
      phaseSeq: '5',
    });
  });
  it('falls back to the global phase only when no decision identity exists', async () => {
    mocks.current = { id: 'gwt_AAAAAAAAAAAAAAAAAAAAAA', phaseSeq: '7', deadline: 0 };
    const { frame } = await mount();
    mocks.run.mockClear();
    const message = {
      type: 'action',
      id: mocks.current.id,
      phaseSeq: '6',
      action: { kind: 'pass' },
    };
    dispatch(frame, message);
    dispatch(frame, { ...message, phaseSeq: '7', decisionID: 'retired' });
    expect(mocks.run).not.toHaveBeenCalled();
    dispatch(frame, { ...message, phaseSeq: '7' });
    expect(mocks.run).toHaveBeenCalledExactlyOnceWith({
      kind: 'action',
      id: message.id,
      phaseSeq: '7',
      decisionID: undefined,
      action: message.action,
    });
  });
  it('rejects foreign senders and stale windows; binds the action to its decision', async () => {
    mocks.current = {
      id: 'gwt_AAAAAAAAAAAAAAAAAAAAAA',
      phaseSeq: '4',
      decisionID: '9',
      deadline: 0,
    };
    const { frame } = await mount();
    mocks.run.mockClear();
    const message = {
      type: 'action',
      id: mocks.current.id,
      phaseSeq: '4',
      decisionID: '9',
      action: { kind: 'pass' },
    };
    dispatch(frame, message, 'https://other.example');
    dispatch(frame, message, location.origin, window);
    dispatch(frame, { ...message, decisionID: '8' });
    expect(mocks.run).not.toHaveBeenCalled();
    dispatch(frame, message);
    expect(mocks.run).toHaveBeenCalledExactlyOnceWith({
      kind: 'action',
      id: message.id,
      phaseSeq: '4',
      decisionID: '9',
      action: { kind: 'pass' },
    });
  });
});
