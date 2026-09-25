import { useEffect, useRef, useState } from 'react';
import { RawErrorViewer } from '@shared/observability/RequestDiagnostics';
import {
  getIndependentDiagnostic,
  getIndependentDiagnostics,
  type IndependentDetail,
  type IndependentItem,
} from '@shared/observability/independentApi';
import { DispatchDetails, OmittedBody } from '@shared/observability/IndependentDiagnostics';
import { usePictureBookText } from '@shared/picturebook/copy';

export function DiscoveryFailureDiagnostics({ operationID }: { readonly operationID: string }) {
  return <OperationDiagnostics key={operationID} operationID={operationID} />;
}

function OperationDiagnostics({ operationID }: { readonly operationID: string }) {
  const t = usePictureBookText();
  const [items, setItems] = useState<IndependentItem[]>([]);
  const [details, setDetails] = useState<Record<string, IndependentDetail>>({});
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [detailBusy, setDetailBusy] = useState<string | null>(null);
  const [failed, setFailed] = useState(false);
  const listRequest = useRef<AbortController | null>(null);
  const detailRequest = useRef<AbortController | null>(null);
  useEffect(
    () => () => {
      listRequest.current?.abort();
      detailRequest.current?.abort();
    },
    [],
  );

  async function load() {
    listRequest.current?.abort();
    detailRequest.current?.abort();
    detailRequest.current = null;
    const request = new AbortController();
    listRequest.current = request;
    setItems([]);
    setDetails({});
    setLoaded(false);
    setDetailBusy(null);
    setBusy(true);
    setFailed(false);
    try {
      const page = await getIndependentDiagnostics(
        'admin',
        { kind: 'image_discovery', subject_id: operationID },
        request.signal,
      );
      if (
        page.data.some((item) => item.kind !== 'image_discovery' || item.subject_id !== operationID)
      ) {
        throw new Error('Unexpected discovery diagnostic');
      }
      if (!request.signal.aborted) {
        setItems(page.data);
        setLoaded(true);
      }
    } catch {
      if (!request.signal.aborted) setFailed(true);
    } finally {
      if (!request.signal.aborted) {
        listRequest.current = null;
        setBusy(false);
      }
    }
  }
  async function loadDetail(id: string) {
    if (busy || detailRequest.current !== null) return;
    const request = new AbortController();
    detailRequest.current = request;
    setDetailBusy(id);
    setFailed(false);
    try {
      const detail = await getIndependentDiagnostic('admin', id, request.signal);
      if (
        detail.item.id !== id ||
        detail.item.kind !== 'image_discovery' ||
        detail.item.subject_id !== operationID
      ) {
        throw new Error('Unexpected discovery diagnostic');
      }
      if (!request.signal.aborted) setDetails((current) => ({ ...current, [id]: detail }));
    } catch {
      if (!request.signal.aborted) {
        // A read failure may mean access has changed. Remove previously loaded bodies.
        setDetails({});
        setItems([]);
        setLoaded(false);
        setFailed(true);
      }
    } finally {
      if (!request.signal.aborted) {
        detailRequest.current = null;
        setDetailBusy(null);
      }
    }
  }
  return (
    <section aria-label={t('保留的图片服务发现诊断', 'Retained image discovery diagnostics')}>
      <button
        className="btn btn-secondary"
        type="button"
        disabled={busy || detailBusy !== null}
        onClick={() => void load()}
      >
        {busy
          ? t('正在加载保留诊断…', 'Loading retained diagnostics…')
          : t('加载保留诊断', 'Load retained diagnostics')}
      </button>
      {failed ? (
        <p role="alert">{t('无法加载保留诊断。', 'Retained diagnostics could not be loaded.')}</p>
      ) : null}
      {loaded && items.length === 0 ? (
        <p>{t('没有可用的保留诊断正文。', 'No retained diagnostic body is available.')}</p>
      ) : null}
      {items.map((item) => {
        const detail = details[item.id];
        return (
          <details key={item.id}>
            <summary>
              {t('尝试', 'Attempt')} {item.attempt_seq} / {t('事件', 'event')} {item.event_seq} ·
              HTTP {item.http_status ?? '—'} · {item.content_type || '—'} · {item.bytes_saved} B
              {item.truncated ? ` · ${t('已截断', 'truncated')}` : ''} · {item.save_state}
            </summary>
            <DispatchDetails dispatch={item.dispatch} />
            {item.save_state !== 'saved' ? <OmittedBody state={item.save_state} /> : null}
            {detail ? (
              detail.body.save_state === 'saved' ? (
                <RawErrorViewer body={detail.body} />
              ) : (
                <OmittedBody state={detail.body.save_state} />
              )
            ) : item.save_state === 'saved' ? (
              <button
                className="btn btn-secondary"
                type="button"
                disabled={busy || detailBusy !== null}
                onClick={() => void loadDetail(item.id)}
              >
                {detailBusy === item.id
                  ? t('正在加载保留响应正文…', 'Loading retained response body…')
                  : t('加载保留响应正文', 'Load retained response body')}
              </button>
            ) : null}
          </details>
        );
      })}
    </section>
  );
}
