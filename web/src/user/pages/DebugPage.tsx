import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { Card, EmptyState, ErrorState, LoadingState, PageHeader, StatusBadge } from '@shared/components/States';
import { ApiError } from '@shared/query/http';
import { formatDateTime } from '@shared/utils/datetime';
import type { DebugObserverStatus } from '../features/debug/v2stream';
import {
  safeRequestJSON,
  valuePresence,
  type DebugEndReason,
  type DebugGapReason,
  type DebugMode,
  type DebugTrace,
  type Presence,
} from '../features/debug/v2types';
import { useDebugV2 } from '../features/debug/useDebugV2';
import '../features/debug/debug-v2.css';
import '@shared/operations/operations.css';

const PARAMETER_ORDER = ['model', 'stream', 'messages', 'temperature', 'top_p', 'max_tokens', 'tools', 'tool_choice', 'response_format'] as const;

const ROUTE_LABEL_KEYS = {
  openai_chat_completions: 'user.debug.state.route.openaiChatCompletions',
  charity_chat_completions: 'user.debug.state.route.charityChatCompletions',
} as const satisfies Record<DebugTrace['request']['route_kind'], string>;

const TRACE_STATE_LABEL_KEYS = {
  capturing: 'user.debug.state.trace.capturing',
  terminal: 'user.debug.state.trace.terminal',
} as const satisfies Record<DebugTrace['state'], string>;

const RESULT_LABEL_KEYS = {
  response: 'user.debug.state.result.response',
  synthetic: 'user.debug.state.result.synthetic',
} as const satisfies Record<NonNullable<DebugTrace['upstream_result']>['result_kind'], string>;

const SOURCE_LABEL_KEYS = {
  platform: 'user.debug.state.source.platform',
  upstream: 'user.debug.state.source.upstream',
} as const satisfies Record<NonNullable<DebugTrace['caller_result']>['source'], string>;

const MODE_LABEL_KEYS = {
  dry: 'user.debug.state.mode.dry',
  live: 'user.debug.state.mode.live',
} as const satisfies Record<DebugMode, string>;

const OBSERVER_LABEL_KEYS = {
  connecting: 'user.debug.state.observer.connecting',
  connected: 'user.debug.state.observer.connected',
  reconnecting: 'user.debug.state.observer.reconnecting',
  disconnected: 'user.debug.state.observer.disconnected',
} as const satisfies Record<DebugObserverStatus, string>;

const GAP_LABEL_KEYS = {
  cursor_invalid: 'user.debug.state.gap.cursorInvalid',
  process_restart: 'user.debug.state.gap.processRestart',
  ring_expired: 'user.debug.state.gap.ringExpired',
  ring_evicted: 'user.debug.state.gap.ringEvicted',
  slow_consumer: 'user.debug.state.gap.slowConsumer',
} as const satisfies Record<DebugGapReason, string>;

const END_LABEL_KEYS = {
  stopped: 'user.debug.state.end.stopped',
  replaced: 'user.debug.state.end.replaced',
  idle_expired: 'user.debug.state.end.idleExpired',
  absolute_expired: 'user.debug.state.end.absoluteExpired',
  auth_revoked: 'user.debug.state.end.authRevoked',
  account_banned: 'user.debug.state.end.accountBanned',
  account_deleted: 'user.debug.state.end.accountDeleted',
  shutdown: 'user.debug.state.end.shutdown',
} as const satisfies Record<DebugEndReason, string>;

const PRESENCE_LABEL_KEYS = {
  missing: 'user.debug.state.presence.missing',
  null: 'user.debug.state.presence.null',
  false: 'user.debug.state.presence.false',
  zero: 'user.debug.state.presence.zero',
  'empty-string': 'user.debug.state.presence.emptyString',
  'empty-array': 'user.debug.state.presence.emptyArray',
  'empty-object': 'user.debug.state.presence.emptyObject',
  value: 'user.debug.state.presence.value',
} as const satisfies Record<Presence, string>;

function plainJSON(value: unknown, unavailable: string): string {
  try {
    return JSON.stringify(value, null, 2) ?? unavailable;
  } catch {
    return unavailable;
  }
}

function RequestDetails({ trace }: { trace: DebugTrace }) {
  const { t } = useTranslation();
  const parsed = safeRequestJSON(trace);
  const root = parsed !== null && typeof parsed === 'object' && !Array.isArray(parsed)
    ? parsed as Record<string, unknown>
    : null;
  const titleId = trace.trace_id + '-request';
  return (
    <section aria-labelledby={titleId}>
      <h3 id={titleId}>{t('user.debug.request.title')}</h3>
      <p>{t('user.debug.request.ownerOnly')}</p>
      <dl className="ops-debug-presence">
        {PARAMETER_ORDER.map((name) => {
          const present = Boolean(root && Object.prototype.hasOwnProperty.call(root, name));
          const value = root?.[name];
          const presence = valuePresence(value, present);
          return (
            <div key={name}>
              <dt>{name}</dt>
              <dd>
                <code>{t(PRESENCE_LABEL_KEYS[presence])}</code>
                {present ? <> · {plainJSON(value, t('user.debug.value.unavailable'))}</> : null}
              </dd>
            </div>
          );
        })}
      </dl>
      <details>
        <summary>{t(trace.request.body.truncated ? 'user.debug.request.rawBodyTruncated' : 'user.debug.request.rawBody', { bytes: trace.request.body.byte_count })}</summary>
        <pre className="ops-debug-json">{trace.request.body.text ?? t('user.debug.request.base64Body', { characters: trace.request.body.base64?.length ?? 0 })}</pre>
      </details>
    </section>
  );
}

function TraceCard({ trace }: { trace: DebugTrace }) {
  const { t } = useTranslation();
  const upstreamTitleId = trace.trace_id + '-upstream';
  const callerTitleId = trace.trace_id + '-caller';
  return (
    <Card className="ops-debug-trace">
      <details>
        <summary>{t(ROUTE_LABEL_KEYS[trace.request.route_kind])} · {trace.request.model} · {t(TRACE_STATE_LABEL_KEYS[trace.state])}</summary>
        <div className="ops-debug-sections">
          <RequestDetails trace={trace} />
          <section aria-labelledby={upstreamTitleId}>
            <h3 id={upstreamTitleId}>{t('user.debug.upstream.title')}</h3>
            {trace.upstream_result ? (
              <dl className="ops-debug-presence">
                <div><dt>{t('user.debug.upstream.kind')}</dt><dd>{t(RESULT_LABEL_KEYS[trace.upstream_result.result_kind])}</dd></div>
                <div><dt>{t('user.debug.upstream.status')}</dt><dd>{trace.upstream_result.status_code ?? t('common.none')}</dd></div>
                <div><dt>{t('user.debug.upstream.safeCode')}</dt><dd>{trace.upstream_result.upstream_code ?? t('common.none')}</dd></div>
                <div><dt>{t('user.debug.upstream.safeDiagnostic')}</dt><dd>{trace.upstream_result.diag ?? t('common.none')}</dd></div>
                <div><dt>{t('user.debug.upstream.charge')}</dt><dd>{trace.upstream_result.usage.charge} {t('common.creditsUnit')}</dd></div>
              </dl>
            ) : <p>{t(trace.state === 'capturing' ? 'user.debug.upstream.awaitingProjection' : 'user.debug.upstream.notDispatched')}</p>}
            <p className="inline-notice">{t('user.debug.upstream.rawSuppressed')}</p>
          </section>
          <section aria-labelledby={callerTitleId}>
            <h3 id={callerTitleId}>{t('user.debug.caller.title')}</h3>
            {trace.caller_result ? (
              <dl className="ops-debug-presence">
                <div><dt>{t('user.debug.caller.httpStatus')}</dt><dd>{trace.caller_result.http_status}</dd></div>
                <div><dt>{t('user.debug.caller.errorCode')}</dt><dd>{trace.caller_result.error_code ?? t('common.none')}</dd></div>
                <div><dt>{t('user.debug.caller.source')}</dt><dd>{t(SOURCE_LABEL_KEYS[trace.caller_result.source])}</dd></div>
                <div><dt>{t('user.debug.caller.message')}</dt><dd>{trace.caller_result.message}</dd></div>
              </dl>
            ) : <p>{t('user.debug.caller.notTerminal')}</p>}
          </section>
        </div>
      </details>
    </Card>
  );
}

function mutationErrorKey(error: unknown): string | null {
  if (error instanceof ApiError && error.status === 409) return 'user.debug.error.sessionChanged';
  if (error instanceof ApiError && error.status === 0) return 'user.debug.error.resultUnknown';
  return null;
}

type Confirmation = 'live' | 'stop' | 'replace' | null;

export function DebugPage() {
  const { t } = useTranslation();
  const debug = useDebugV2();
  const [confirmation, setConfirmation] = useState<Confirmation>(null);
  const session = debug.view.session;
  const localizedMutationErrorKey = debug.mutationError ? mutationErrorKey(debug.mutationError) : null;
  const mutationErrorMessage = localizedMutationErrorKey
    ? t(localizedMutationErrorKey)
    : debug.mutationError instanceof Error
      ? debug.mutationError.message
      : t('user.debug.error.operationFailed');
  const confirmationCopy = useMemo(() => ({
    live: [
      t('user.debug.confirm.liveTitle'),
      t('user.debug.confirm.liveBody'),
      t('user.debug.confirm.liveAction'),
    ],
    stop: [
      t('user.debug.confirm.stopTitle'),
      t(session.active && session.inflight_count > 0 ? 'user.debug.confirm.stopInflightBody' : 'user.debug.confirm.stopIdleBody'),
      t('user.debug.confirm.stopAction'),
    ],
    replace: [
      t('user.debug.confirm.replaceTitle'),
      t(session.active && session.inflight_count > 0 ? 'user.debug.confirm.replaceInflightBody' : 'user.debug.confirm.replaceIdleBody'),
      t('user.debug.confirm.replaceAction'),
    ],
  } as const), [session, t]);

  if (debug.authority.isPending) {
    return <div className="page"><PageHeader title={t('user.debug.nav')} description={t('user.debug.descriptionShort')} /><LoadingState /></div>;
  }
  if (debug.authority.error && !debug.authority.data) {
    return <div className="page"><PageHeader title={t('user.debug.nav')} description={t('user.debug.descriptionShort')} /><ErrorState error={debug.authority.error} onRetry={() => void debug.authority.refetch()} /></div>;
  }

  return (
    <div className="page ops-page">
      <PageHeader
        title={t('user.debug.nav')}
        description={t('user.debug.description')}
        actions={!session.active ? <button className="btn btn-primary" type="button" disabled={debug.mutating} onClick={() => void debug.start()}>{t('user.debug.actions.start')}</button> : null}
      />
      {debug.authority.error ? <ErrorState error={debug.authority.error} onRetry={() => void debug.authority.refetch()} /> : null}
      <Card>
        <h2>{t('user.debug.connection.title')}</h2>
        <div className="ops-debug-status">
          <div><strong>{t('user.debug.connection.serverSession')}</strong><p><StatusBadge active={session.active} label={t(session.active ? 'user.debug.state.session.active' : 'user.debug.state.session.stopped')} /></p></div>
          <div><strong>{t('user.debug.connection.observer')}</strong><p><StatusBadge active={debug.observer === 'connected'} label={t(OBSERVER_LABEL_KEYS[debug.observer])} /></p></div>
          <div><strong>{t('user.debug.connection.mode')}</strong><p>{session.active ? t(MODE_LABEL_KEYS[session.mode]) : t('common.none')}</p></div>
          <div><strong>{t('user.debug.connection.inFlight')}</strong><p>{session.active ? session.inflight_count : 0}</p></div>
        </div>
        {session.active ? <p>{t('user.debug.connection.expiry', { expires: formatDateTime(session.expires_at), idleExpiry: formatDateTime(session.idle_expires_at), revision: session.revision })}</p> : null}
        {debug.view.gap ? <p className="inline-notice" role="status">{t('user.debug.connection.gap', { reason: t(GAP_LABEL_KEYS[debug.view.gap]) })}</p> : null}
        {debug.view.end ? <p className="inline-notice" role="status">{t('user.debug.connection.ended', { reason: t(END_LABEL_KEYS[debug.view.end]), count: debug.view.cancelled_inflight_count })}</p> : null}
        {debug.streamError ? <ErrorState error={debug.streamError} onRetry={debug.connect} /> : null}
      </Card>

      {session.active ? (
        <Card className="ops-danger-zone">
          <h2>{t('user.debug.operations.title')}</h2>
          <div className="ops-debug-actions">
            {debug.observer === 'disconnected'
              ? <button className="btn btn-secondary" type="button" onClick={debug.connect}>{t('user.debug.operations.attachObserver')}</button>
              : <button className="btn btn-secondary" type="button" onClick={debug.disconnect}>{t('user.debug.operations.disconnectObserver')}</button>}
            {session.mode === 'dry'
              ? <button className="btn btn-secondary" type="button" disabled={debug.mutating} onClick={() => setConfirmation('live')}>{t('user.debug.operations.enableLive')}</button>
              : <button className="btn btn-secondary" type="button" disabled={debug.mutating} onClick={() => void debug.setMode('dry', session.revision)}>{t('user.debug.operations.returnDry')}</button>}
            <button className="btn btn-danger" type="button" disabled={debug.mutating} onClick={() => setConfirmation('stop')}>{t('user.debug.operations.stop')}</button>
            <button className="btn btn-danger" type="button" disabled={debug.mutating} onClick={() => setConfirmation('replace')}>{t('user.debug.operations.startOver')}</button>
          </div>
          <p>{t('user.debug.operations.observerHint')}</p>
          {debug.mutationError ? <p className="field-error" role="alert">{mutationErrorMessage}</p> : null}
        </Card>
      ) : null}

      <Card>
        <h2>{t('user.debug.fixedResults.title')}</h2>
        <p><code>debug_dry_run_intercepted</code> (422): {t('user.debug.fixedResults.dryIntercepted')}</p>
        <p><code>debug_live_result_captured</code> (422): {t('user.debug.fixedResults.liveCaptured')}</p>
        <p><code>debug_live_cancelled</code> (422): {t('user.debug.fixedResults.liveCancelled')}</p>
        <p>{t('user.debug.fixedResults.callerKeyPrefix')} <code>&#36;NONBIRI_CALLER_KEY</code> {t('user.debug.fixedResults.callerKeySuffix')}</p>
      </Card>

      <section className="ops-debug-traces" aria-labelledby="debug-traces-title">
        <h2 id="debug-traces-title">{t('user.debug.requests.title')}</h2>
        {!session.active
          ? <EmptyState title={t('user.debug.requests.noSessionTitle')} body={t('user.debug.requests.noSessionBody')} />
          : debug.view.traces.length === 0
            ? <EmptyState title={t('user.debug.requests.emptyTitle')} body={t('user.debug.requests.emptyBody')} />
            : debug.view.traces.map((trace) => <TraceCard key={trace.trace_id} trace={trace} />)}
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
