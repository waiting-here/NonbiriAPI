import { useMemo, useState } from 'react';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { Card, EmptyState, ErrorState, LoadingState, PageHeader, StatusBadge } from '@shared/components/States';
import { formatDateTime } from '@shared/utils/datetime';
import { safeRequestJSON, valuePresence, type DebugTrace } from '../features/debug/v2types';
import { debugMutationMessage, useDebugV2 } from '../features/debug/useDebugV2';
import '../features/debug/debug-v2.css';
import '@shared/operations/operations.css';

const PARAMETER_ORDER = ['model', 'stream', 'messages', 'temperature', 'top_p', 'max_tokens', 'tools', 'tool_choice', 'response_format'] as const;

function plainJSON(value: unknown): string {
  try { return JSON.stringify(value, null, 2); } catch { return '[unavailable]'; }
}

function RequestDetails({ trace }: { trace: DebugTrace }) {
  const parsed = safeRequestJSON(trace);
  const root = parsed !== null && typeof parsed === 'object' && !Array.isArray(parsed)
    ? parsed as Record<string, unknown>
    : null;
  return (
    <section aria-labelledby={`${trace.trace_id}-request`}>
      <h3 id={`${trace.trace_id}-request`}>Request</h3>
      <p>Owner-submitted request only. Headers and credentials are never captured.</p>
      <dl className="ops-debug-presence">
        {PARAMETER_ORDER.map((name) => {
          const present = Boolean(root && Object.prototype.hasOwnProperty.call(root, name));
          const value = root?.[name];
          return <div key={name}><dt>{name}</dt><dd><code>{valuePresence(value, present)}</code>{present ? ` · ${plainJSON(value)}` : ''}</dd></div>;
        })}
      </dl>
      <details>
        <summary>Bounded raw request body ({trace.request.body.byte_count} bytes{trace.request.body.truncated ? ', truncated' : ''})</summary>
        <pre className="ops-debug-json">{trace.request.body.text ?? `[base64 request body: ${trace.request.body.base64?.length ?? 0} characters]`}</pre>
      </details>
    </section>
  );
}

function TraceCard({ trace }: { trace: DebugTrace }) {
  return (
    <Card className="ops-debug-trace">
      <details>
        <summary>{trace.request.route_kind} · {trace.request.model} · {trace.state}</summary>
        <div className="ops-debug-sections">
          <RequestDetails trace={trace} />
          <section aria-labelledby={`${trace.trace_id}-upstream`}>
            <h3 id={`${trace.trace_id}-upstream`}>Upstream</h3>
            {trace.upstream_result ? (
              <dl className="ops-debug-presence">
                <div><dt>Kind</dt><dd>{trace.upstream_result.result_kind}</dd></div>
                <div><dt>Status</dt><dd>{trace.upstream_result.status_code ?? 'none'}</dd></div>
                <div><dt>Safe code</dt><dd>{trace.upstream_result.upstream_code ?? 'none'}</dd></div>
                <div><dt>Safe diagnostic</dt><dd>{trace.upstream_result.diag ?? 'none'}</dd></div>
                <div><dt>Charge</dt><dd>{trace.upstream_result.usage.charge} credits</dd></div>
              </dl>
            ) : <p>{trace.state === 'capturing' ? 'Awaiting a safe result projection.' : 'No upstream dispatch occurred.'}</p>}
            <p className="inline-notice">Raw upstream headers, bodies, bytes, and messages are never available here.</p>
          </section>
          <section aria-labelledby={`${trace.trace_id}-caller`}>
            <h3 id={`${trace.trace_id}-caller`}>Caller</h3>
            {trace.caller_result ? (
              <dl className="ops-debug-presence">
                <div><dt>HTTP status</dt><dd>{trace.caller_result.http_status}</dd></div>
                <div><dt>Error code</dt><dd>{trace.caller_result.error_code ?? 'none'}</dd></div>
                <div><dt>Source</dt><dd>{trace.caller_result.source}</dd></div>
                <div><dt>Message</dt><dd>{trace.caller_result.message}</dd></div>
              </dl>
            ) : <p>Caller result is not terminal yet.</p>}
          </section>
        </div>
      </details>
    </Card>
  );
}

type Confirmation = 'live' | 'stop' | 'replace' | null;

export function DebugPage() {
  const debug = useDebugV2();
  const [confirmation, setConfirmation] = useState<Confirmation>(null);
  const session = debug.view.session;
  const confirmationCopy = useMemo(() => ({
    live: ['Enable live capture?', 'Live mode may dispatch a real upstream request and incur upstream cost. Raw upstream data remains suppressed.', 'Enable live'],
    stop: ['Stop this Debug session?', session.active && session.inflight_count > 0 ? 'Stopping cancels in-flight live requests and callers receive the fixed live-cancelled result.' : 'Stopping ends the server session and its bounded in-memory traces.', 'Stop session'],
    replace: ['Replace this Debug session?', session.active && session.inflight_count > 0 ? 'Replacing cancels in-flight live requests. The new session always starts in Dry mode.' : 'Existing bounded traces are discarded. The new session always starts in Dry mode.', 'Replace session'],
  } as const), [session]);

  if (debug.authority.isPending) return <div className="page"><PageHeader title="Debug" description="Inspect the safe request lifecycle." /><LoadingState /></div>;
  if (debug.authority.error && !debug.authority.data) return <div className="page"><PageHeader title="Debug" description="Inspect the safe request lifecycle." /><ErrorState error={debug.authority.error} onRetry={() => void debug.authority.refetch()} /></div>;

  return (
    <div className="page ops-page">
      <PageHeader title="Debug" description="Capture a bounded, owner-only view of request, upstream, and caller semantics." actions={!session.active ? <button className="btn btn-primary" type="button" disabled={debug.mutating} onClick={() => void debug.start()}>Start Debug</button> : null} />
      {debug.authority.error ? <ErrorState error={debug.authority.error} onRetry={() => void debug.authority.refetch()} /> : null}
      <Card>
        <h2>Connection and mode</h2>
        <div className="ops-debug-status">
          <div><strong>Server session</strong><p><StatusBadge active={session.active} label={session.active ? 'active' : 'stopped'} /></p></div>
          <div><strong>Observer</strong><p><StatusBadge active={debug.observer === 'connected'} label={debug.observer} /></p></div>
          <div><strong>Mode</strong><p>{session.active ? session.mode : 'none'}</p></div>
          <div><strong>In flight</strong><p>{session.active ? session.inflight_count : 0}</p></div>
        </div>
        {session.active ? <p>Expires {formatDateTime(session.expires_at)} · idle expiry {formatDateTime(session.idle_expires_at)} · revision {session.revision}</p> : null}
        {debug.view.gap ? <p className="inline-notice" role="status">Event continuity gap: {debug.view.gap}. A fresh authoritative snapshot is required.</p> : null}
        {debug.view.end ? <p className="inline-notice" role="status">Session ended: {debug.view.end}; cancelled in flight: {debug.view.cancelled_inflight_count}.</p> : null}
        {debug.streamError ? <ErrorState error={debug.streamError} onRetry={debug.connect} /> : null}
      </Card>

      {session.active ? (
        <Card className="ops-danger-zone">
          <h2>Session operations</h2>
          <div className="ops-debug-actions">
            {debug.observer === 'disconnected' ? <button className="btn btn-secondary" type="button" onClick={debug.connect}>Attach observer</button> : <button className="btn btn-secondary" type="button" onClick={debug.disconnect}>Disconnect observer</button>}
            {session.mode === 'dry' ? <button className="btn btn-secondary" type="button" disabled={debug.mutating} onClick={() => setConfirmation('live')}>Enable live mode</button> : <button className="btn btn-secondary" type="button" disabled={debug.mutating} onClick={() => void debug.setMode('dry', session.revision)}>Return to Dry</button>}
            <button className="btn btn-danger" type="button" disabled={debug.mutating} onClick={() => setConfirmation('stop')}>Stop session</button>
            <button className="btn btn-danger" type="button" disabled={debug.mutating} onClick={() => setConfirmation('replace')}>Start over</button>
          </div>
          <p>Disconnecting this observer never stops the server session. Stop and Start over are explicit server mutations.</p>
          {debug.mutationError ? <p className="field-error" role="alert">{debugMutationMessage(debug.mutationError)}</p> : null}
        </Card>
      ) : null}

      <Card>
        <h2>Fixed caller results</h2>
        <p><code>debug_dry_run_intercepted</code> (422): captured in Dry mode and never sent upstream.</p>
        <p><code>debug_live_result_captured</code> (422): an upstream attempt completed, but its raw response was suppressed and only the safe projection was captured.</p>
        <p>A stopped or replaced dispatched live request uses the distinct fixed <code>debug_live_cancelled</code> result.</p>
        <p>Examples use <code>$NONBIRI_CALLER_KEY</code> as a placeholder. A one-time CallerKey is never copied into Debug automatically.</p>
      </Card>

      <section className="ops-debug-traces" aria-labelledby="debug-traces-title">
        <h2 id="debug-traces-title">Requests</h2>
        {!session.active ? <EmptyState title="No active Debug session" body="Start a Dry session to capture requests without upstream egress." /> : debug.view.traces.length === 0 ? <EmptyState title="No captured requests" body="Send an OpenAI-compatible request while this session is active." /> : debug.view.traces.map((trace) => <TraceCard key={trace.trace_id} trace={trace} />)}
      </section>

      {confirmation && session.active ? (
        <ConfirmDialog open title={confirmationCopy[confirmation][0]} description={confirmationCopy[confirmation][1]} confirmLabel={confirmationCopy[confirmation][2]} danger busy={debug.mutating} onCancel={() => setConfirmation(null)} onConfirm={() => {
          const current = confirmation;
          setConfirmation(null);
          if (current === 'live') void debug.setMode('live', session.revision);
          else if (current === 'stop') void debug.stop(session.revision, session.inflight_count > 0);
          else void debug.replace(session.revision, session.inflight_count > 0);
        }} />
      ) : null}
    </div>
  );
}
