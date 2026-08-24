import { screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { renderWithProviders } from '../../../../test/unit/support';
import { DebugPage } from '../../pages/DebugPage';
import * as sessionData from '../../data';
import * as debugHook from './useDebugSession';
import type { DebugSessionMetadata } from './types';

vi.mock('../../data', () => ({ useUserSession: vi.fn() }));
vi.mock('./useDebugSession', () => ({ useDebugSession: vi.fn() }));

const limits = {
  max_sessions: 64,
  hub_bytes: 128,
  session_bytes: 4,
  max_traces: 32,
  max_events: 128,
  event_bytes: 512,
  subscriber_queue: 64,
  max_subscribers: 2,
  raw_request_bytes: 64,
  messages_tools_bytes: 128,
  parameters_bytes: 64,
  effective_summary_bytes: 64,
  response_bytes: 256,
  trace_bytes: 768,
  first_attach_seconds: 30,
  reconnect_seconds: 30,
  idle_seconds: 600,
  absolute_seconds: 3_600,
  heartbeat_seconds: 15,
  write_deadline_seconds: 15,
  confirmation_seconds: 60,
} as const;

const metadata: DebugSessionMetadata = {
  id: 'dbg_abcdefghijklmnopqrstuv',
  generation: 1,
  mode: 'dry',
  created_at: 1_700_000_000,
  expires_at: 1_700_003_600,
  idle_expires_at: 1_700_000_600,
  connected: true,
  last_event_id: 1,
  limits,
};

describe('DebugPage', () => {
  it('renders the warning, bounded parameter semantics, and feature-local copy', async () => {
    vi.mocked(sessionData.useUserSession).mockReturnValue({
      data: { user: {} },
      error: null,
      isPending: false,
      refetch: vi.fn(),
    } as never);
    vi.mocked(debugHook.useDebugSession).mockReturnValue({
      metadata,
      connection: 'connected',
      mode: 'dry',
      traces: [
        {
          id: 'debug-request-1',
          revision: 1,
          payload: {
            request_id: 'debug-request-1',
            model: 'public/model',
            parameters: [
              {
                name: 'user',
                presence: 'empty_string',
                type: 'string',
                source: 'caller',
                value: '',
              },
              {
                name: 'temperature',
                presence: 'value',
                type: 'number',
                source: 'caller',
                value: 0.2,
                changed: true,
              },
            ],
            effective: {
              temperature: {
                caller_value: 0.2,
                effective: { state: 'applied', value: 0.1 },
              },
            },
          },
        },
      ],
      lastEventId: 1,
      dropped: 0,
      gapReason: null,
      endReason: null,
      error: null,
      busy: false,
      start: vi.fn(async () => undefined),
      stop: vi.fn(async () => undefined),
      setDry: vi.fn(async () => undefined),
      enableLive: vi.fn(async () => undefined),
      clearView: vi.fn(),
    });

    const rendered = await renderWithProviders(<DebugPage />, {
      station: 'user',
      role: 'user',
      locale: 'en',
    });
    expect(screen.getByRole('heading', { name: 'Request debugger' })).toBeInTheDocument();
    expect(screen.getByText(/Starting a debug session changes/)).toBeInTheDocument();
    expect(screen.getByRole('table')).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Type' })).toBeInTheDocument();
    expect(screen.getByText('empty string')).toBeInTheDocument();
    expect(screen.getByText('Caller / Policy')).toBeInTheDocument();
    expect(screen.getAllByText('Unknown').length).toBeGreaterThan(0);
    expect(rendered.container.querySelector('.debug-parameter-scroll')).toBeInTheDocument();
    expect(rendered.container.querySelector('[dangerouslysetinnerhtml]')).toBeNull();
  });

  it('localizes status, source, and effective-state labels in Chinese', async () => {
    vi.mocked(sessionData.useUserSession).mockReturnValue({
      data: { user: {} },
      error: null,
      isPending: false,
      refetch: vi.fn(),
    } as never);
    vi.mocked(debugHook.useDebugSession).mockReturnValue({
      metadata,
      connection: 'connected',
      mode: 'dry',
      traces: [
        {
          id: 'debug-request-zh',
          revision: 1,
          payload: {
            request_id: 'debug-request-zh',
            model: 'public/model',
            mode: 'dry',
            terminal: 'received',
            parameters: [
              {
                name: 'temperature',
                presence: 'value',
                type: 'number',
                source: 'caller',
                value: 0.2,
              },
            ],
            effective: {
              temperature: {
                caller_value: 0.2,
                effective: { state: 'applied', value: 0.1 },
              },
            },
          },
        },
      ],
      lastEventId: 1,
      dropped: 0,
      gapReason: null,
      endReason: null,
      error: null,
      busy: false,
      start: vi.fn(async () => undefined),
      stop: vi.fn(async () => undefined),
      setDry: vi.fn(async () => undefined),
      enableLive: vi.fn(async () => undefined),
      clearView: vi.fn(),
    } as never);

    await renderWithProviders(<DebugPage />, {
      station: 'user',
      role: 'user',
      locale: 'zh',
    });
    expect(screen.getByRole('heading', { name: '请求调试器' })).toBeInTheDocument();
    expect(screen.getByText('调用方 / 策略')).toBeInTheDocument();
    expect(screen.getAllByText('已接收').length).toBeGreaterThan(0);
    expect(screen.getByText('已应用: 0.1')).toBeInTheDocument();
  });
});
