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
import './observability.css';

function useWords() {
  const { i18n } = useTranslation();
  return (en: string, zh: string) => (i18n.resolvedLanguage?.startsWith('zh') ? zh : en);
}

export function RequestSource(props: { role: DiagnosticRole; requestID: string }) {
  return <ScopedRequestSource key={props.role + ':' + props.requestID} {...props} />;
}

function ScopedRequestSource({ role, requestID }: { role: DiagnosticRole; requestID: string }) {
  const words = useWords();
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
  if (error)
    return <p role="status">{words('Source details are unavailable.', '来源详情暂不可用。')}</p>;
  if (!source) return <p>{words('No source details recorded.', '没有已记录的来源详情。')}</p>;
  return (
    <section className="request-diagnostics" aria-label={words('Request source', '请求来源')}>
      <p>{words('Client headers are self-reported clues.', '客户端请求头是客户端自报线索。')}</p>
      <dl>
        {Object.entries(source)
          .filter(([key]) => key !== 'quality')
          .map(([key, value]) => (
            <div key={key}>
              <dt>{key}</dt>
              <dd>
                {String(value)}{' '}
                {source.quality?.[key] && (
                  <span>
                    {Object.entries(source.quality[key])
                      .filter(([, flag]) => flag)
                      .map(([flag]) => flag)
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
  const words = useWords();
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
      <button type="button" disabled={busy} onClick={() => void load()}>
        {words('Upstream error details', '上游错误详情')}
      </button>
      {failed && <p role="alert">{words('Could not load diagnostics.', '无法加载错误详情。')}</p>}
      {page && page.data.length === 0 && (
        <p>{words('No error body was recorded.', '没有已记录的错误正文。')}</p>
      )}
      {page?.data.map((error) => (
        <details key={error.event_seq}>
          <summary>
            {words('Error event', '错误事件')} {error.event_seq} · HTTP {error.http_status ?? '—'} ·{' '}
            {error.bytes_saved} B {error.truncated ? words('(truncated)', '（已截断）') : ''}
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
                ? words(
                    'Raw body was not saved because the storage budget was full.',
                    '原文因容量不足未保存。',
                  )
                : words('Raw body could not be saved.', '原文读取或保存失败。')}
            </p>
          )}
        </details>
      ))}
      {page?.next_after != null && (
        <button type="button" disabled={busy} onClick={() => void load(page.next_after!)}>
          {words('Next events', '后续事件')}
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
  const words = useWords();
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
      <button type="button" disabled={busy} onClick={() => void load()}>
        {words('Load original body', '加载原始正文')}
      </button>
      {failed && (
        <p role="alert">{words('Could not load the error body.', '无法加载错误正文。')}</p>
      )}
    </>
  );
}

export function RawErrorViewer({ body }: { body: ErrorBody }) {
  const words = useWords();
  const raw = new TextDecoder().decode(errorBytes(body));
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
        } else
          setNotice(
            words(
              'Formatting is unavailable; use the original text or download.',
              '无法格式化，请查看原文或下载。',
            ),
          );
      };
      current.onerror = () => {
        finish();
        setNotice(words('Formatting is unavailable.', '格式化暂不可用。'));
      };
      timer.current = setTimeout(() => {
        finish();
        setNotice(words('Formatting timed out.', '格式化超时。'));
      }, 2000);
      current.postMessage({ raw, truncated: body.truncated });
    } catch {
      setFormatting(false);
      setNotice(words('Formatting is unavailable.', '格式化暂不可用。'));
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
      {body.truncated && (
        <p role="status">{words('The body was truncated at 1 MiB.', '正文已在 1 MiB 处截断。')}</p>
      )}
      {body.encoding === 'base64' && (
        <p>
          {words(
            'Non-UTF-8 bytes are replaced for display. Download preserves the original bytes.',
            '非 UTF-8 字节以替换字符显示；下载保留原始字节。',
          )}
        </p>
      )}
      <div className="diagnostic-actions">
        <button
          type="button"
          disabled={body.truncated || body.encoding === 'base64' || formatting}
          onClick={format}
        >
          {words('Format JSON', '格式化 JSON')}
        </button>
        {formatted && (
          <button type="button" onClick={() => setShowRaw((value) => !value)}>
            {showRaw ? words('Formatted', '格式化') : words('Original', '原文')}
          </button>
        )}
        <button
          type="button"
          onClick={() => {
            void copyText(showRaw ? raw : (formatted ?? raw)).then((ok) =>
              setNotice(ok ? words('Copied.', '已复制。') : words('Copy failed.', '复制失败。')),
            );
          }}
        >
          {words('Copy text', '复制文本')}
        </button>
        <button type="button" onClick={download}>
          {words('Download original', '下载原始正文')}
        </button>
      </div>
      <details open={body.bytes_saved <= 16_384}>
        <summary>{words('Body preview', '正文预览')}</summary>
        <pre>{(showRaw ? raw : (formatted ?? raw)).slice(0, 65_536)}</pre>
        {(showRaw ? raw : (formatted ?? raw)).length > 65_536 && (
          <p>
            {words(
              'Preview shows the first 65,536 characters. Copy or download for the complete body.',
              '预览仅展示前 65,536 个字符，可复制或下载完整正文。',
            )}
          </p>
        )}
      </details>
      {notice && <p role="status">{notice}</p>}
    </div>
  );
}
