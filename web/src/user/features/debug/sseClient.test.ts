import { describe, expect, it, vi } from 'vitest';
import { connectDebugEvents, DebugStreamError } from './sseClient';

function streamResponse(body: string): Response {
  const stream = new ReadableStream<Uint8Array>({
    start(controller) {
      controller.enqueue(new TextEncoder().encode(body));
      controller.close();
    },
  });
  return new Response(stream, {
    status: 200,
    headers: { 'content-type': 'text/event-stream; charset=utf-8' },
  });
}

const snapshot = {
  version: 1,
  seq: 7,
  type: 'session_snapshot',
  session_id: 'dbg_abcdefghijklmnopqrstuv',
  trace_id: '',
  revision: 0,
  at: 1_700_000_000,
  payload: { metadata: {}, traces: [] },
};

describe('Debug SSE client', () => {
  it('sends Last-Event-ID as a header and rejects cursor mismatch', async () => {
    const fetchMock = vi.fn<typeof fetch>(() =>
      Promise.resolve(
        streamResponse(`id: 8\nevent: session_snapshot\ndata: ${JSON.stringify(snapshot)}\n\n`),
      ),
    );
    vi.stubGlobal('fetch', fetchMock);
    const onInvalid = vi.fn();
    await expect(
      connectDebugEvents({ lastEventId: 7, onEvent: vi.fn(), onInvalid }),
    ).rejects.toBeInstanceOf(DebugStreamError);
    expect(onInvalid.mock.calls[0]?.[0]).toBe('event_id_mismatch');
    const request = fetchMock.mock.calls[0]?.[1];
    const headers = new Headers(request?.headers);
    expect(headers.get('Last-Event-ID')).toBe('7');
    expect(String(fetchMock.mock.calls[0]?.[0])).not.toContain('dbg_abcdefghijklmnopqrstuv');
  });

  it('stops at an unknown event instead of applying a later trace delta', async () => {
    const unknown = { ...snapshot, seq: 1, type: 'future_event' };
    const trace = {
      ...snapshot,
      seq: 2,
      type: 'trace_upsert',
      trace_id: 'trace-1',
      revision: 1,
      payload: { trace: { terminal: 'completed' } },
    };
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(() =>
        Promise.resolve(
          streamResponse(
            `id: 1\nevent: future_event\ndata: ${JSON.stringify(unknown)}\n\nid: 2\nevent: trace_upsert\ndata: ${JSON.stringify(trace)}\n\n`,
          ),
        ),
      ),
    );
    const onEvent = vi.fn();
    const onInvalid = vi.fn();
    await expect(connectDebugEvents({ onEvent, onInvalid })).rejects.toBeInstanceOf(
      DebugStreamError,
    );
    expect(onInvalid.mock.calls[0]?.[0]).toBe('invalid_envelope');
    expect(onInvalid.mock.calls[0]?.[1]).toEqual({
      seq: 1,
      sessionID: 'dbg_abcdefghijklmnopqrstuv',
    });
    expect(onEvent).not.toHaveBeenCalled();
  });

  it('supports comments, CRLF, and multiple data lines without durable side effects', async () => {
    const event = { ...snapshot, seq: 1 };
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(() =>
        Promise.resolve(
          streamResponse(
            `: heartbeat\r\nid: 1\r\nevent: session_snapshot\r\ndata: ${JSON.stringify(event)}\r\n\r\n`,
          ),
        ),
      ),
    );
    const onEvent = vi.fn();
    await connectDebugEvents({ onEvent });
    expect(onEvent).toHaveBeenCalledTimes(1);
    expect(onEvent.mock.calls[0]?.[0].seq).toBe(1);
  });

  it('rejects duplicate SSE control fields and an inexact media type', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(() =>
        Promise.resolve(
          new Response('', { status: 200, headers: { 'content-type': 'text/event-streamish' } }),
        ),
      ),
    );
    await expect(connectDebugEvents({ onEvent: vi.fn() })).rejects.toBeInstanceOf(DebugStreamError);

    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(() =>
        Promise.resolve(
          streamResponse(
            `id: 1\nid: 1\nevent: session_snapshot\ndata: ${JSON.stringify({ ...snapshot, seq: 1 })}\n\n`,
          ),
        ),
      ),
    );
    const onInvalid = vi.fn();
    await expect(connectDebugEvents({ onEvent: vi.fn(), onInvalid })).rejects.toBeInstanceOf(
      DebugStreamError,
    );
    expect(onInvalid.mock.calls[0]?.[0]).toBe('duplicate_id_field');
  });

  it('rejects an event body over the bounded wire cap before JSON parsing', async () => {
    const onInvalid = vi.fn();
    const oversized = 'x'.repeat(512 * 1024);
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(() =>
        Promise.resolve(streamResponse(`event: future_event\ndata: ${oversized}\n\n`)),
      ),
    );
    await expect(connectDebugEvents({ onEvent: vi.fn(), onInvalid })).rejects.toBeInstanceOf(
      DebugStreamError,
    );
    expect(onInvalid.mock.calls[0]?.[0]).toBe('event_too_large');
  });

  it('resets the event budget after each dispatch', async () => {
    const first = {
      ...snapshot,
      seq: 1,
      payload: { pad: 'x'.repeat(300 * 1024) },
    };
    const second = {
      ...snapshot,
      seq: 2,
      payload: { pad: 'y'.repeat(300 * 1024) },
    };
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(() =>
        Promise.resolve(
          streamResponse(
            `id: 1\nevent: session_snapshot\ndata: ${JSON.stringify(first)}\n\nid: 2\nevent: session_snapshot\ndata: ${JSON.stringify(second)}\n\n`,
          ),
        ),
      ),
    );
    const onEvent = vi.fn();
    await connectDebugEvents({ onEvent });
    expect(onEvent).toHaveBeenCalledTimes(2);
  });

  it('rejects non-snapshot trace identity and revision combinations', async () => {
    const invalid = { ...snapshot, seq: 1, trace_id: 'trace-1', revision: 1 };
    vi.stubGlobal(
      'fetch',
      vi.fn<typeof fetch>(() =>
        Promise.resolve(
          streamResponse(`id: 1\nevent: session_snapshot\ndata: ${JSON.stringify(invalid)}\n\n`),
        ),
      ),
    );
    const onInvalid = vi.fn();
    await expect(connectDebugEvents({ onEvent: vi.fn(), onInvalid })).rejects.toBeInstanceOf(
      DebugStreamError,
    );
    expect(onInvalid.mock.calls[0]?.[0]).toBe('invalid_envelope');
  });
});
