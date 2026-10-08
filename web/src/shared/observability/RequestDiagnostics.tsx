import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { copyText } from '@shared/utils/clipboard';
import {
  errorBytes,
  getErrorBody,
  getErrorList,
  getSource,
  type DiagnosticRole,
  type ErrorBody,
  type ErrorPage,
  type SourceFacts,
} from './api';
import type { FormattedError } from './formatError';
import { sourceFlagLabel, sourceQualityLabel } from './sourceLabels';
import { fieldLabel, riskCopy } from '@shared/riskAudit/copy';
import './observability.css';

export function RequestSource(props: { role: DiagnosticRole; requestID: string }) {
  return <ScopedRequestSource key={props.role + ':' + props.requestID} {...props} />;
}

function ScopedRequestSource({ role, requestID }: { role: DiagnosticRole; requestID: string }) {
  const { t } = useTranslation();
  const labels = riskCopy(t);
  const [source, setSource] = useState<SourceFacts | null>();
  const [error, setError] = useState(false);
  useEffect(() => {
    const controller = new AbortController();
    getSource(role, requestID, controller.signal)
      .then(setSource)
      .catch(() => {
        if (!controller.signal.aborted) setError(true);
      });
    return () => controller.abort();
  }, [role, requestID]);
  if (error) return <p role="status">{t('common.diagnostics.sourceDetailsAreUnavailable')}</p>;
  if (!source) return <p>{t('common.diagnostics.noSourceDetailsRecorded')}</p>;
  return (
    <section className="request-diagnostics" aria-label={t('common.diagnostics.requestSource')}>
      <p>{t('common.diagnostics.clientHeadersAreSelfreportedClues')}</p>
      <dl>
        {Object.entries(source)
          .filter(([key]) => key !== 'quality')
          .map(([key, value]) => (
            <div key={key}>
              <dt>{key === 'ip_quality' ? labels.quality : fieldLabel(key, labels)}</dt>
              <dd>
                {key === 'ip_quality' ? sourceQualityLabel(String(value), t) : String(value)}{' '}
                {source.quality?.[key] && (
                  <span>
                    {Object.entries(source.quality[key])
                      .filter(([, flag]) => flag)
                      .map(([flag]) => sourceFlagLabel(flag, t))
                      .join(', ')}
                  </span>
                )}
              </dd>
            </div>
          ))}
      </dl>
    </section>
  );
}

export function AttemptErrors(props: { role: DiagnosticRole; requestID: string; attempt: number }) {
  return (
    <ScopedAttemptErrors
      key={props.role + ':' + props.requestID + ':' + props.attempt}
      {...props}
    />
  );
}

function ScopedAttemptErrors({
  role,
  requestID,
  attempt,
}: {
  role: DiagnosticRole;
  requestID: string;
  attempt: number;
}) {
  const { t } = useTranslation();
  const [page, setPage] = useState<ErrorPage>();
  const [failed, setFailed] = useState(false);
  const [busy, setBusy] = useState(false);
  const controller = useRef<AbortController | null>(null);
  useEffect(() => () => controller.current?.abort(), []);
  async function load(after = 0) {
    controller.current?.abort();
    const request = new AbortController();
    controller.current = request;
    setBusy(true);
    setFailed(false);
    try {
      const result = await getErrorList(role, requestID, attempt, after, request.signal);
      if (!request.signal.aborted) setPage(result);
    } catch {
      if (!request.signal.aborted) setFailed(true);
    } finally {
      if (!request.signal.aborted) setBusy(false);
    }
  }
  return (
    <section className="request-diagnostics">
      <button
        className="nb-btn nb-btn--secondary"
        type="button"
        disabled={busy}
        onClick={() => void load()}
      >
        {t('common.diagnostics.upstreamErrorDetails')}
      </button>
      {failed && <p role="alert">{t('common.diagnostics.couldNotLoadDiagnostics')}</p>}
      {page && page.data.length === 0 && <p>{t('common.diagnostics.noErrorBodyWasRecorded')}</p>}
      {page?.data.map((error) => (
        <details key={error.event_seq}>
          <summary>
            {t('common.diagnostics.errorEvent')} {error.event_seq} · HTTP {error.http_status ?? '—'}{' '}
            · {error.bytes_saved} B {error.truncated ? t('common.diagnostics.truncated') : ''}
          </summary>
          {error.save_state === 'saved' ? (
            <LazyErrorBody
              role={role}
              requestID={requestID}
              attempt={attempt}
              event={error.event_seq}
            />
          ) : (
            <p>
              {error.save_state === 'capacity_exhausted'
                ? t('common.diagnostics.rawBodyWasNotSavedBecauseThe')
                : error.failure_reason === 'read_failure'
                  ? t('common.diagnostics.rawBodyReadFailed')
                  : error.failure_reason === 'storage_failure'
                    ? t('common.diagnostics.rawBodyStorageFailed')
                    : t('common.diagnostics.rawBodyUnspecifiedFailure')}
            </p>
          )}
        </details>
      ))}
      {page?.next_after != null && (
        <button
          className="nb-btn nb-btn--secondary"
          type="button"
          disabled={busy}
          onClick={() => void load(page.next_after!)}
        >
          {t('common.diagnostics.nextEvents')}
        </button>
      )}
    </section>
  );
}

function LazyErrorBody({
  role,
  requestID,
  attempt,
  event,
}: {
  role: DiagnosticRole;
  requestID: string;
  attempt: number;
  event: number;
}) {
  const { t } = useTranslation();
  const [body, setBody] = useState<ErrorBody>();
  const [failed, setFailed] = useState(false);
  const [busy, setBusy] = useState(false);
  const controller = useRef<AbortController | null>(null);
  useEffect(() => () => controller.current?.abort(), []);
  async function load() {
    controller.current?.abort();
    const request = new AbortController();
    controller.current = request;
    setBusy(true);
    setFailed(false);
    try {
      const result = await getErrorBody(role, requestID, attempt, event, request.signal);
      errorBytes(result);
      if (!request.signal.aborted) setBody(result);
    } catch {
      if (!request.signal.aborted) setFailed(true);
    } finally {
      if (!request.signal.aborted) setBusy(false);
    }
  }
  return body ? (
    <RawErrorViewer body={body} />
  ) : (
    <>
      <button
        className="nb-btn nb-btn--secondary"
        type="button"
        disabled={busy}
        onClick={() => void load()}
      >
        {t('common.diagnostics.loadOriginalBody')}
      </button>
      {failed && <p role="alert">{t('common.diagnostics.couldNotLoadTheErrorBody')}</p>}
    </>
  );
}

export function RawErrorViewer({ body }: { body: ErrorBody }) {
  const { t } = useTranslation();
  const raw = new TextDecoder().decode(errorBytes(body));
  const synthetic = body.content_type === 'application/vnd.nonbiriapi.image-diagnostic+json';
  const [formatted, setFormatted] = useState<string>();
  const [formatting, setFormatting] = useState(false);
  const [notice, setNotice] = useState('');
  const [showRaw, setShowRaw] = useState(true);
  const worker = useRef<Worker | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(
    () => () => {
      worker.current?.terminate();
      if (timer.current) clearTimeout(timer.current);
    },
    [],
  );
  function format() {
    worker.current?.terminate();
    if (timer.current) clearTimeout(timer.current);
    setFormatting(true);
    setNotice('');
    try {
      const current = new Worker(new URL('./errorFormatter.worker.ts', import.meta.url), {
        type: 'module',
      });
      worker.current = current;
      const finish = () => {
        current.terminate();
        setFormatting(false);
        if (timer.current) clearTimeout(timer.current);
      };
      current.onmessage = (event: MessageEvent<FormattedError>) => {
        finish();
        if (event.data.text !== undefined) {
          setFormatted(event.data.text);
          setShowRaw(false);
        } else setNotice(t('common.diagnostics.formattingIsUnavailableUseTheOriginalText'));
      };
      current.onerror = () => {
        finish();
        setNotice(t('common.diagnostics.formattingIsUnavailable'));
      };
      timer.current = setTimeout(() => {
        finish();
        setNotice(t('common.diagnostics.formattingTimedOut'));
      }, 2000);
      current.postMessage({ raw, truncated: body.truncated });
    } catch {
      setFormatting(false);
      setNotice(t('common.diagnostics.formattingIsUnavailable'));
    }
  }
  function download() {
    const url = URL.createObjectURL(
      new Blob([errorBytes(body)], { type: 'application/octet-stream' }),
    );
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = `upstream-error-${body.event_seq}.bin`;
    anchor.click();
    setTimeout(() => URL.revokeObjectURL(url), 0);
  }
  return (
    <div className="request-diagnostics">
      {synthetic && (
        <p role="status">{t('common.diagnostics.thisIsASafeDiagnosticSummaryGenerated')}</p>
      )}
      {body.truncated && <p role="status">{t('common.diagnostics.theBodyWasTruncatedAt1Mib')}</p>}
      {body.encoding === 'base64' && (
        <p>{t('common.diagnostics.nonutf8BytesAreReplacedForDisplayDownload')}</p>
      )}
      <div className="diagnostic-actions">
        <button
          className="nb-btn nb-btn--secondary"
          type="button"
          disabled={body.truncated || body.encoding === 'base64' || formatting}
          onClick={format}
        >
          {t('common.diagnostics.formatJson')}
        </button>
        {formatted && (
          <button
            className="nb-btn nb-btn--secondary"
            type="button"
            onClick={() => setShowRaw((value) => !value)}
          >
            {showRaw
              ? t('common.diagnostics.formatted')
              : synthetic
                ? t('common.diagnostics.summaryText')
                : t('common.diagnostics.original')}
          </button>
        )}
        <button
          className="nb-btn nb-btn--secondary"
          type="button"
          onClick={() => {
            void copyText(showRaw ? raw : (formatted ?? raw)).then((ok) =>
              setNotice(ok ? t('common.diagnostics.copied') : t('common.diagnostics.copyFailed')),
            );
          }}
        >
          {t('common.diagnostics.copyText')}
        </button>
        <button className="nb-btn nb-btn--secondary" type="button" onClick={download}>
          {synthetic
            ? t('common.diagnostics.downloadDiagnosticSummary')
            : t('common.diagnostics.downloadOriginal')}
        </button>
      </div>
      <details open={body.bytes_saved <= 16_384}>
        <summary>{t('common.diagnostics.bodyPreview')}</summary>
        <pre>{(showRaw ? raw : (formatted ?? raw)).slice(0, 65_536)}</pre>
        {(showRaw ? raw : (formatted ?? raw)).length > 65_536 && (
          <p>{t('common.diagnostics.previewShowsTheFirst65536CharactersCopy')}</p>
        )}
      </details>
      {notice && <p role="status">{notice}</p>}
    </div>
  );
}
