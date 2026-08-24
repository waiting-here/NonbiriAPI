import { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { Card, EmptyState, PageHeader } from '@shared/components/States';
import { UserPageGate } from '../components/UserPageGate';
import { debugCopyForLanguage, type DebugCopy } from '../features/debug/copy';
import {
  effectiveParameter,
  parametersForTrace,
  stableValueKey,
} from '../features/debug/parameterProjection';
import { useDebugSession } from '../features/debug/useDebugSession';
import type { DebugTrace, DebugTraceRecord } from '../features/debug/types';
import '../features/debug/debug.css';

type StatusFilter =
  | 'all'
  | 'received'
  | 'validated'
  | 'routing'
  | 'dispatching'
  | 'streaming'
  | 'completed'
  | 'dry_completed'
  | 'failed'
  | 'cancelled'
  | 'incomplete';
type TimeFilter = 'all' | 'hour' | 'day';

function textValue(value: unknown): string {
  if (typeof value === 'string') return value;
  if (value === undefined) return '';
  try {
    return JSON.stringify(value, null, 2) ?? '';
  } catch {
    return '[unavailable]';
  }
}

function compactText(value: unknown, fallback = '—'): string {
  const text = textValue(value).replace(/\s+/g, ' ').trim();
  return text || fallback;
}

function traceStatus(trace: DebugTrace): string {
  return typeof trace.terminal === 'string' && TRACE_STATUS_VALUES.has(trace.terminal)
    ? trace.terminal
    : 'unknown';
}

function traceMode(trace: DebugTrace): string {
  return trace.mode === 'dry' || trace.mode === 'live' ? trace.mode : 'unknown';
}

const TRACE_STATUS_VALUES = new Set([
  'received',
  'validated',
  'routing',
  'dispatching',
  'streaming',
  'completed',
  'dry_completed',
  'failed',
  'cancelled',
  'incomplete',
]);

function traceModel(trace: DebugTrace): string {
  return typeof trace.model === 'string' ? trace.model : '—';
}

function timestamp(value: unknown): string {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value <= 0) return '—';
  try {
    return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'medium' }).format(
      new Date(value * 1000),
    );
  } catch {
    return '—';
  }
}

function statusLabel(status: string, copy: DebugCopy): string {
  const labels: Record<string, string> = {
    received: copy.statusReceived,
    validated: copy.statusValidated,
    routing: copy.statusRouting,
    dispatching: copy.statusDispatching,
    streaming: copy.statusStreaming,
    completed: copy.statusCompleted,
    dry_completed: copy.statusDryCompleted,
    failed: copy.statusFailed,
    cancelled: copy.statusCancelled,
    incomplete: copy.statusIncomplete,
  };
  return labels[status] ?? copy.unknown;
}

function sourceLabel(source: string, copy: DebugCopy): string {
  return source
    .split(' / ')
    .map((part) => {
      if (part === 'caller') return copy.caller;
      if (part === 'policy') return copy.policy;
      return part;
    })
    .join(' / ');
}

function projectionStateLabel(state: unknown, copy: DebugCopy): string {
  if (state === 'applied') return copy.applied;
  if (state === 'unchanged') return copy.unchanged;
  return compactText(state);
}

function effectiveLabel(value: unknown, copy: DebugCopy): string {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return compactText(value);
  const record = value as Record<string, unknown>;
  const nested = record.effective;
  if (nested && typeof nested === 'object' && !Array.isArray(nested)) {
    const projection = nested as Record<string, unknown>;
    const state = projection.state;
    if (state === 'not_selected_not_evaluated') return copy.notEvaluated;
    if (state === 'hidden' || projection.value_hidden === true) return copy.hidden;
    if (state === 'applied' || state === 'unchanged') {
      return projection.value === undefined
        ? projectionStateLabel(state, copy)
        : `${projectionStateLabel(state, copy)}: ${compactText(projection.value)}`;
    }
    return compactText(projection.state ?? projection);
  }
  const state = record.state;
  if (state === 'not_selected_not_evaluated') return copy.notEvaluated;
  if (record.value_hidden === true) return copy.hidden;
  if (state === 'applied' || state === 'unchanged') {
    return record.value === undefined
      ? projectionStateLabel(state, copy)
      : `${projectionStateLabel(state, copy)}: ${compactText(record.value)}`;
  }
  return compactText(value);
}

function effectiveValue(value: unknown): unknown {
  if (value && typeof value === 'object' && !Array.isArray(value) && 'effective' in value) {
    const nested = (value as Record<string, unknown>).effective;
    if (nested && typeof nested === 'object' && !Array.isArray(nested) && 'value' in nested) {
      return (nested as Record<string, unknown>).value;
    }
  }
  return value;
}

function changedParameter(caller: unknown, effective: unknown): boolean {
  if (effective === undefined) return false;
  const projection = effectiveValue(effective);
  if (effective && typeof effective === 'object' && !Array.isArray(effective)) {
    const state = (effective as Record<string, unknown>).effective;
    if (state && typeof state === 'object' && !Array.isArray(state)) {
      const stateName = (state as Record<string, unknown>).state;
      if (stateName === 'applied' || stateName === 'hidden') return true;
    }
  }
  return stableValueKey(caller) !== stableValueKey(projection);
}

function CopySection({
  title,
  value,
  copy,
  onCopy,
  open = false,
}: {
  title: string;
  value: unknown;
  copy: DebugCopy;
  onCopy: (title: string, value: unknown) => void;
  open?: boolean;
}) {
  const rendered = textValue(value);
  if (!rendered) return null;
  return (
    <details className="debug-copy-section" open={open}>
      <summary className="debug-section-heading">
        <h3>{title}</h3>
      </summary>
      <div className="debug-section-heading">
        <span className="sr-only">{title}</span>
        <button type="button" className="btn btn-secondary" onClick={() => onCopy(title, value)}>
          {copy.copy}
        </button>
      </div>
      <pre className="debug-code debug-raw">{rendered}</pre>
    </details>
  );
}

function ParameterTable({
  trace,
  copy,
  onCopy,
}: {
  trace: DebugTrace;
  copy: DebugCopy;
  onCopy: (title: string, value: unknown) => void;
}) {
  const parameters = parametersForTrace(trace);
  return (
    <div className="debug-parameter-scroll">
      <table className="debug-parameter-table">
        <thead>
          <tr>
            <th scope="col">{copy.field}</th>
            <th scope="col">{copy.type}</th>
            <th scope="col">{copy.source}</th>
            <th scope="col">{copy.presence}</th>
            <th scope="col">{copy.callerValue}</th>
            <th scope="col">{copy.effectiveValue}</th>
            <th scope="col">{copy.changed}</th>
            <th scope="col">{copy.truncatedField}</th>
          </tr>
        </thead>
        <tbody>
          {parameters.map((parameter) => {
            const effective = effectiveParameter(trace, parameter.name);
            const effectiveText = effective === undefined ? '—' : effectiveLabel(effective, copy);
            const changed = parameter.changed || changedParameter(parameter.value, effective);
            const source = sourceLabel(
              effective === undefined ? parameter.source : `${parameter.source} / policy`,
              copy,
            );
            return (
              <tr key={parameter.name}>
                <th scope="row">{parameter.name}</th>
                <td>{parameter.type}</td>
                <td>{source}</td>
                <td className="debug-presence">{presenceLabel(parameter.presence, copy)}</td>
                <td>
                  <span className="debug-parameter-value">
                    {parameter.value === undefined ? '—' : textValue(parameter.value)}
                  </span>
                </td>
                <td>
                  <span className="debug-parameter-value">{effectiveText}</span>
                  {effective !== undefined ? (
                    <button
                      type="button"
                      className="btn btn-secondary btn-small"
                      onClick={() => onCopy(`${parameter.name} ${copy.effectiveSuffix}`, effective)}
                    >
                      {copy.copy}
                    </button>
                  ) : null}
                </td>
                <td>{changed ? 'true' : 'false'}</td>
                <td>{parameter.truncated ? 'true' : 'false'}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function presenceLabel(presence: string, copy: DebugCopy): string {
  switch (presence) {
    case 'absent':
      return copy.absent;
    case 'null':
      return copy.nullValue;
    case 'false':
      return copy.falseValue;
    case 'zero':
      return copy.zero;
    case 'empty_string':
      return copy.emptyString;
    case 'empty_array':
      return copy.emptyArray;
    case 'empty_object':
      return copy.emptyObject;
    default:
      return copy.value;
  }
}

function DebugTraceDetail({
  traceRecord,
  copy,
  onCopy,
}: {
  traceRecord: DebugTraceRecord;
  copy: DebugCopy;
  onCopy: (title: string, value: unknown) => void;
}) {
  const [view, setView] = useState<'structured' | 'raw'>('structured');
  const trace = traceRecord.payload;
  const effective = trace.effective;
  const flags = [
    trace.truncated === true ? copy.truncated : '',
    trace.dropped !== undefined ? copy.dropped(Number(trace.dropped) || 0) : '',
    trace.incomplete === true ? copy.incomplete : '',
    traceStatus(trace) === 'cancelled' ? copy.cancelled : '',
  ].filter(Boolean);
  return (
    <Card className="debug-detail-card">
      <div className="debug-card-title">
        <h2>{copy.details}</h2>
        <div className="form-actions">
          <button
            type="button"
            className={`btn ${view === 'structured' ? 'btn-primary' : 'btn-secondary'}`}
            onClick={() => setView('structured')}
          >
            {copy.structured}
          </button>
          <button
            type="button"
            className={`btn ${view === 'raw' ? 'btn-primary' : 'btn-secondary'}`}
            onClick={() => setView('raw')}
          >
            {copy.raw}
          </button>
        </div>
      </div>
      {flags.length > 0 ? <p className="debug-inline-notice">{flags.join(' · ')}</p> : null}
      {view === 'raw' ? (
        <CopySection title={copy.raw} value={trace} copy={copy} onCopy={onCopy} open />
      ) : (
        <div className="debug-details">
          <section>
            <h3>{copy.summary}</h3>
            <dl className="debug-summary-grid">
              <div>
                <dt>request_id</dt>
                <dd className="mono">{compactText(trace.request_id)}</dd>
              </div>
              <div>
                <dt>received_at</dt>
                <dd>{timestamp(trace.received_at)}</dd>
              </div>
              <div>
                <dt>mode</dt>
                <dd>
                  {traceMode(trace) === 'live'
                    ? copy.liveBadge
                    : traceMode(trace) === 'dry'
                      ? copy.dryBadge
                      : copy.unknown}
                </dd>
              </div>
              <div>
                <dt>route</dt>
                <dd>{compactText(trace.route)}</dd>
              </div>
              <div>
                <dt>model</dt>
                <dd>{traceModel(trace)}</dd>
              </div>
              <div>
                <dt>status</dt>
                <dd>
                  <span className="debug-status-text" data-status={traceStatus(trace)}>
                    {statusLabel(traceStatus(trace), copy)}
                  </span>
                </dd>
              </div>
              <div>
                <dt>HTTP status</dt>
                <dd>{compactText(trace.status)}</dd>
              </div>
              <div>
                <dt>revision</dt>
                <dd>{traceRecord.revision}</dd>
              </div>
            </dl>
          </section>
          <section>
            <h3>{copy.timeline}</h3>
            <dl className="debug-summary-grid">
              <div>
                <dt>terminal</dt>
                <dd>{compactText(trace.terminal)}</dd>
              </div>
              <div>
                <dt>attempt</dt>
                <dd>{compactText(trace.attempt_state)}</dd>
              </div>
              <div>
                <dt>commit stage</dt>
                <dd>{compactText(trace.commit_stage)}</dd>
              </div>
              <div>
                <dt>error</dt>
                <dd>{compactText(trace.error)}</dd>
              </div>
            </dl>
          </section>
          <section>
            <h3>{copy.parameters}</h3>
            <ParameterTable trace={trace} copy={copy} onCopy={onCopy} />
          </section>
          <CopySection title={copy.messages} value={trace.messages} copy={copy} onCopy={onCopy} />
          <CopySection title={copy.tools} value={trace.tools} copy={copy} onCopy={onCopy} />
          <CopySection title={copy.effective} value={effective} copy={copy} onCopy={onCopy} />
          <CopySection title={copy.response} value={trace.response} copy={copy} onCopy={onCopy} />
          <CopySection
            title={copy.callerJSON}
            value={trace.raw_request}
            copy={copy}
            onCopy={onCopy}
          />
        </div>
      )}
    </Card>
  );
}

function sessionStatusLabel(connection: string, copy: DebugCopy): string {
  switch (connection) {
    case 'starting':
      return copy.starting;
    case 'connecting':
      return copy.connecting;
    case 'connected':
      return copy.connected;
    case 'reconnecting':
      return copy.reconnecting;
    case 'stopped':
      return copy.stopped;
    case 'replaced':
      return copy.replaced;
    case 'expired':
      return copy.expired;
    case 'error':
      return copy.error;
    default:
      return copy.noSession;
  }
}

function DebugConsole() {
  const { i18n } = useTranslation();
  const copy = debugCopyForLanguage(i18n.language);
  const debug = useDebugSession();
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [search, setSearch] = useState('');
  const [status, setStatus] = useState<StatusFilter>('all');
  const [mode, setMode] = useState<'all' | 'dry' | 'live'>('all');
  const [model, setModel] = useState('');
  const [time, setTime] = useState<TimeFilter>('all');
  const [follow, setFollow] = useState(true);
  const [selectedID, setSelectedID] = useState<string | null>(null);
  const [copyNotice, setCopyNotice] = useState('');
  const [now, setNow] = useState(0);

  const active = Boolean(debug.metadata);
  const filteredTraces = useMemo(() => {
    const normalizedSearch = search.trim().toLowerCase();
    const normalizedModel = model.trim().toLowerCase();
    const cutoff = time === 'hour' ? now - 3_600 : time === 'day' ? now - 86_400 : 0;
    return debug.traces.filter((record) => {
      const trace = record.payload;
      const statusValue = traceStatus(trace);
      const modeValue = traceMode(trace);
      const receivedAt = typeof trace.received_at === 'number' ? trace.received_at : 0;
      if (status !== 'all' && statusValue !== status) return false;
      if (mode !== 'all' && modeValue !== mode) return false;
      if (normalizedModel && !traceModel(trace).toLowerCase().includes(normalizedModel))
        return false;
      if (
        normalizedSearch &&
        !`${record.id} ${trace.request_id ?? ''} ${traceModel(trace)}`
          .toLowerCase()
          .includes(normalizedSearch)
      )
        return false;
      if (cutoff > 0 && receivedAt > 0 && receivedAt < cutoff) return false;
      return true;
    });
  }, [debug.traces, model, mode, now, search, status, time]);

  useEffect(() => {
    const updateClock = () => setNow(Date.now() / 1000);
    const initialTimer = window.setTimeout(updateClock, 0);
    const interval = window.setInterval(updateClock, 60_000);
    return () => {
      window.clearTimeout(initialTimer);
      window.clearInterval(interval);
    };
  }, []);

  const fallbackSelectedID = filteredTraces[filteredTraces.length - 1]?.id ?? null;
  const selectedIDForView =
    follow || !filteredTraces.some((record) => record.id === selectedID)
      ? fallbackSelectedID
      : selectedID;
  const selectedTrace = filteredTraces.find((record) => record.id === selectedIDForView) ?? null;

  const copySection = useCallback(
    async (title: string, value: unknown) => {
      const text = textValue(value);
      if (!text || !navigator.clipboard) {
        setCopyNotice(`${title}: ${copy.copyFailed}`);
        return;
      }
      try {
        await navigator.clipboard.writeText(text);
        setCopyNotice(`${title}: ${copy.copied}`);
      } catch {
        setCopyNotice(`${title}: ${copy.copyFailed}`);
      }
    },
    [copy],
  );

  return (
    <div className="page debug-page">
      <PageHeader eyebrow={copy.eyebrow} title={copy.title} description={copy.description} />
      <section className="debug-warning" aria-label={copy.warningTitle}>
        <span className="debug-warning-icon" aria-hidden="true">
          !
        </span>
        <div>
          <h2>{copy.warningTitle}</h2>
          <p>{copy.warningBody}</p>
          <p>{copy.sessionInfluence}</p>
        </div>
      </section>
      <Card>
        <div className="debug-toolbar">
          <span className="debug-connection" data-state={debug.connection} role="status">
            {sessionStatusLabel(debug.connection, copy)}
          </span>
          <span className={`debug-mode ${debug.mode === 'live' ? 'is-live' : ''}`}>
            {debug.mode === 'live' ? copy.liveBadge : copy.dryBadge}
          </span>
          <button
            type="button"
            className="btn btn-primary"
            onClick={() => void debug.start()}
            disabled={debug.busy}
          >
            {active ? copy.replace : copy.start}
          </button>
          {active ? (
            <>
              <button
                type="button"
                className="btn btn-danger"
                onClick={() => void debug.stop()}
                disabled={debug.busy}
              >
                {copy.stop}
              </button>
              <button
                type="button"
                className={`debug-mode ${debug.mode === 'live' ? 'is-live' : ''}`}
                role="switch"
                aria-checked={debug.mode === 'live'}
                disabled={debug.busy || debug.connection !== 'connected'}
                onClick={() => {
                  if (debug.mode === 'live') void debug.setDry();
                  else setConfirmOpen(true);
                }}
              >
                {debug.mode === 'live' ? copy.actualEnabled : copy.enableActual}
              </button>
            </>
          ) : null}
        </div>
        {debug.error ? (
          <p className="debug-error" role="alert">
            {debug.error}
          </p>
        ) : null}
        {debug.gapReason ? (
          <p className="debug-inline-notice" role="status">
            <strong>{copy.gap}</strong> · {copy.gapBody}
          </p>
        ) : null}
        {debug.dropped > 0 ? (
          <p className="debug-inline-notice" role="status">
            {copy.dropped(debug.dropped)}
          </p>
        ) : null}
      </Card>
      {debug.metadata ? <SessionMetadata metadata={debug.metadata} copy={copy} /> : null}
      {!active ? (
        <>
          <EmptyState
            title={copy.noSession}
            body={copy.noSessionBody}
            action={
              <button
                type="button"
                className="btn btn-primary"
                onClick={() => void debug.start()}
                disabled={debug.busy}
              >
                {copy.start}
              </button>
            }
          />
          <Card>
            <h2>{copy.externalClient}</h2>
            <p className="debug-hint">{copy.startHelp}</p>
            <h3>{copy.curlLabel}</h3>
            <pre className="debug-code">{`curl https://your-nonbiri.example/v1/chat/completions \\\n  -H "Authorization: Bearer ${copy.callerKeyPlaceholder}" \\\n  -H "Content-Type: application/json" \\\n  -d '{"model":"your-model","messages":[{"role":"user","content":"hello"}]}'`}</pre>
          </Card>
        </>
      ) : (
        <>
          <Card>
            <h2>{copy.controls}</h2>
            <div className="debug-filter-grid">
              <label>
                <span>{copy.search}</span>
                <input
                  value={search}
                  onChange={(event) => setSearch(event.currentTarget.value)}
                  placeholder={copy.searchPlaceholder}
                />
              </label>
              <label>
                <span>{copy.statusFilter}</span>
                <select
                  value={status}
                  onChange={(event) => setStatus(event.currentTarget.value as StatusFilter)}
                >
                  <option value="all">{copy.all}</option>
                  {[
                    'received',
                    'validated',
                    'routing',
                    'dispatching',
                    'streaming',
                    'completed',
                    'dry_completed',
                    'failed',
                    'cancelled',
                    'incomplete',
                  ].map((item) => (
                    <option key={item} value={item}>
                      {statusLabel(item, copy)}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                <span>{copy.modeFilter}</span>
                <select
                  value={mode}
                  onChange={(event) => setMode(event.currentTarget.value as 'all' | 'dry' | 'live')}
                >
                  <option value="all">{copy.all}</option>
                  <option value="dry">{copy.dry}</option>
                  <option value="live">{copy.live}</option>
                </select>
              </label>
              <label>
                <span>{copy.modelFilter}</span>
                <input value={model} onChange={(event) => setModel(event.currentTarget.value)} />
              </label>
              <label>
                <span>{copy.timeFilter}</span>
                <select
                  value={time}
                  onChange={(event) => setTime(event.currentTarget.value as TimeFilter)}
                >
                  <option value="all">{copy.all}</option>
                  <option value="hour">{copy.oneHour}</option>
                  <option value="day">{copy.day}</option>
                </select>
              </label>
            </div>
            <div className="debug-toggle-row">
              <label className="debug-switch-label">
                <input
                  type="checkbox"
                  checked={follow}
                  onChange={(event) => setFollow(event.currentTarget.checked)}
                />
                {copy.follow}
              </label>
              <button type="button" className="btn btn-secondary" onClick={debug.clearView}>
                {copy.clearView}
              </button>
            </div>
          </Card>
          <div className="debug-main-grid">
            <Card className="debug-list-card">
              <div className="debug-card-title">
                <h2>{copy.requestList}</h2>
                <span className="muted">{copy.requestCount(filteredTraces.length)}</span>
              </div>
              {filteredTraces.length === 0 ? (
                <EmptyState title={copy.noRequests} body={copy.noRequestsBody} />
              ) : (
                <ul className="debug-list" aria-label={copy.requestList}>
                  {filteredTraces.map((record) => {
                    const trace = record.payload;
                    return (
                      <li className="debug-list-item" key={record.id}>
                        <button
                          type="button"
                          className="debug-list-button"
                          aria-current={record.id === selectedIDForView ? 'true' : undefined}
                          onClick={() => {
                            setFollow(false);
                            setSelectedID(record.id);
                          }}
                        >
                          <span className="debug-list-primary">
                            <span>{traceModel(trace)}</span>
                            <span className="debug-status-text" data-status={traceStatus(trace)}>
                              {statusLabel(traceStatus(trace), copy)}
                            </span>
                          </span>
                          <span className="debug-list-secondary">
                            <span className="mono">{compactText(trace.request_id, record.id)}</span>
                            <span>{timestamp(trace.received_at)}</span>
                          </span>
                        </button>
                      </li>
                    );
                  })}
                </ul>
              )}
            </Card>
            {selectedTrace ? (
              <DebugTraceDetail
                traceRecord={selectedTrace}
                copy={copy}
                onCopy={(title, value) => void copySection(title, value)}
              />
            ) : (
              <div className="debug-empty-detail">{copy.noRequestsBody}</div>
            )}
          </div>
          {copyNotice ? (
            <p className="debug-copy-notice" role="status" aria-live="polite">
              {copyNotice}
            </p>
          ) : null}
        </>
      )}
      <ConfirmDialog
        open={confirmOpen}
        title={copy.confirmTitle}
        description={copy.confirmBody}
        confirmLabel={copy.confirmEnable}
        danger
        busy={debug.busy}
        onCancel={() => {
          if (!debug.busy) setConfirmOpen(false);
        }}
        onConfirm={() => {
          void debug.enableLive().finally(() => setConfirmOpen(false));
        }}
      />
    </div>
  );
}

function SessionMetadata({
  metadata,
  copy,
}: {
  metadata: NonNullable<ReturnType<typeof useDebugSession>['metadata']>;
  copy: DebugCopy;
}) {
  return (
    <Card>
      <h2>{copy.sessionInfo}</h2>
      <dl className="debug-metadata-grid">
        <div>
          <dt>{copy.sessionID}</dt>
          <dd className="mono">{metadata.id}</dd>
        </div>
        <div>
          <dt>{copy.generation}</dt>
          <dd>{metadata.generation}</dd>
        </div>
        <div>
          <dt>{copy.created}</dt>
          <dd>{timestamp(metadata.created_at)}</dd>
        </div>
        <div>
          <dt>{copy.expires}</dt>
          <dd>{timestamp(metadata.expires_at)}</dd>
        </div>
        <div>
          <dt>{copy.idleExpires}</dt>
          <dd>{timestamp(metadata.idle_expires_at)}</dd>
        </div>
        <div>
          <dt>{copy.lastEvent}</dt>
          <dd>{metadata.last_event_id}</dd>
        </div>
      </dl>
      <h3>{copy.resourceBudget}</h3>
      <dl className="debug-limit-grid">
        {Object.entries(metadata.limits).map(([name, value]) => (
          <div key={name}>
            <dt>{name.replace(/_/g, ' ')}</dt>
            <dd>{String(value)}</dd>
          </div>
        ))}
      </dl>
    </Card>
  );
}

export function DebugPage() {
  return (
    <UserPageGate>
      <DebugConsole />
    </UserPageGate>
  );
}
