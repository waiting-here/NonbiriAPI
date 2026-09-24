import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { RawErrorViewer } from './RequestDiagnostics';
import type { DiagnosticRole } from './api';
import {
  getIndependentDiagnostic,
  getIndependentDiagnostics,
  type IndependentDetail,
  type IndependentFilter,
  type IndependentKind,
  type IndependentPage,
} from './independentApi';
import './observability.css';
interface Props {
  role: DiagnosticRole;
  accountId?: string | number;
  scopeReady: boolean;
  enabled?: boolean;
}
export function IndependentDiagnostics(props: Props) {
  if (!props.scopeReady || props.enabled === false || props.accountId === undefined) return null;
  return <ScopedDiagnostics key={`${props.role}:${props.accountId}`} role={props.role} />;
}
function useWords() {
  const { i18n } = useTranslation();
  return (en: string, zh: string) => (i18n.resolvedLanguage?.startsWith('zh') ? zh : en);
}
function ScopedDiagnostics({ role }: { role: DiagnosticRole }) {
  const words = useWords();
  const [kind, setKind] = useState<IndependentKind>('all');
  const [user, setUser] = useState('');
  const [subject, setSubject] = useState('');
  const [hours, setHours] = useState('24');
  const [page, setPage] = useState<IndependentPage>();
  const [applied, setApplied] = useState<IndependentFilter>();
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);
  const controller = useRef<AbortController | null>(null);
  useEffect(() => () => controller.current?.abort(), []);
  const labels = {
    all: words('All categories', '全部类别'),
    model_discovery: words('Model discovery', '模型列表拉取'),
    image_task: words('Image generation and polling', '绘本生成与查询'),
    image_discovery: words('Image model discovery', '绘本模型拉取'),
  };
  async function load(next = false) {
    controller.current?.abort();
    const request = new AbortController();
    controller.current = request;
    setBusy(true);
    setFailed(false);
    const to = Math.floor(Date.now() / 1000);
    const filter: IndependentFilter =
      next && applied && page?.next_before
        ? { ...applied, before: page.next_before }
        : {
            kind,
            user_id: user.trim() || undefined,
            subject_id: subject.trim() || undefined,
            from: to - Number(hours) * 3600,
            to,
          };
    try {
      const result = await getIndependentDiagnostics(role, filter, request.signal);
      if (!request.signal.aborted) {
        setPage(result);
        setApplied(filter);
      }
    } catch {
      if (!request.signal.aborted) {
        setFailed(true);
        setPage(undefined);
      }
    } finally {
      if (!request.signal.aborted) setBusy(false);
    }
  }
  return (
    <section
      className="request-diagnostics independent-diagnostics"
      aria-label={words('Discovery and activity diagnostics', '模型拉取与活动诊断')}
    >
      <h2>{words('Discovery and activity diagnostics', '模型拉取与活动诊断')}</h2>
      <p>
        {words(
          'Failure details from the last 30 days. Only administrators and full stewards can read these records.',
          '最近30天内的失败详情，仅管理员和6级协管可查看。',
        )}
      </p>
      <form
        className="diagnostic-filters"
        onSubmit={(event) => {
          event.preventDefault();
          void load();
        }}
      >
        <label>
          {words('Category', '类别')}
          <select value={kind} onChange={(event) => setKind(event.target.value as IndependentKind)}>
            {(Object.keys(labels) as IndependentKind[]).map((key) => (
              <option key={key} value={key}>
                {labels[key]}
              </option>
            ))}
          </select>
        </label>
        <label>
          {words('User ID', '用户 ID')}
          <input
            value={user}
            maxLength={19}
            inputMode="numeric"
            pattern="[1-9][0-9]{0,18}"
            onChange={(event) => setUser(event.target.value)}
          />
        </label>
        <label>
          {words('Request, task or operation ID', '请求、任务或操作 ID')}
          <input
            value={subject}
            maxLength={27}
            onChange={(event) => setSubject(event.target.value)}
          />
        </label>
        <label>
          {words('Period', '时间范围')}
          <select value={hours} onChange={(event) => setHours(event.target.value)}>
            <option value="24">{words('Last 24 hours', '最近24小时')}</option>
            <option value="168">{words('Last 7 days', '最近7天')}</option>
            <option value="720">{words('Last 30 days', '最近30天')}</option>
          </select>
        </label>
        <button type="submit" disabled={busy}>
          {words('Search diagnostics', '查询诊断')}
        </button>
      </form>
      {failed && (
        <p role="alert">
          {words(
            'Diagnostics could not be loaded. Check the filters and your current access.',
            '无法加载诊断，请检查筛选条件和当前权限。',
          )}
        </p>
      )}
      {page?.data.length === 0 && (
        <p>{words('No matching diagnostics.', '没有符合条件的诊断。')}</p>
      )}
      <div key={applied ? JSON.stringify(applied) : 'empty'}>
        {page?.data.map((entry) => (
          <details key={entry.id}>
            <summary>
              {labels[entry.kind]} · {entry.subject_id} · {words('User', '用户')} {entry.user_id} ·{' '}
              {new Date(entry.created_at * 1000).toLocaleString()}
              {' · '}
              {words('Attempt', '尝试')} {entry.attempt_seq} / {entry.event_seq} · HTTP{' '}
              {entry.http_status ?? '—'}
              {entry.truncated ? words(' · truncated', ' · 已截断') : ''}
              {entry.synthetic ? words(' · safe diagnostic', ' · 安全诊断') : ''}
            </summary>
            {entry.save_state !== 'saved' && <OmittedBody state={entry.save_state} />}
            <Detail role={role} id={entry.id} synthetic={entry.synthetic} />
          </details>
        ))}
      </div>
      {page?.next_before && (
        <button type="button" disabled={busy} onClick={() => void load(true)}>
          {words('Next diagnostics', '下一页诊断')}
        </button>
      )}
    </section>
  );
}
function OmittedBody({ state }: { state: string }) {
  const words = useWords();
  return (
    <p role="status">
      {state === 'capacity_exhausted'
        ? words(
            'Raw body was not saved because the storage budget was full.',
            '原文因容量不足未保存。',
          )
        : words('Raw body could not be read or saved.', '原文读取或保存失败。')}
    </p>
  );
}
function Detail({ role, id, synthetic }: { role: DiagnosticRole; id: string; synthetic: boolean }) {
  const words = useWords();
  const [detail, setDetail] = useState<IndependentDetail>();
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);
  const controller = useRef<AbortController | null>(null);
  useEffect(() => () => controller.current?.abort(), []);
  async function load() {
    controller.current?.abort();
    const request = new AbortController();
    controller.current = request;
    setBusy(true);
    setFailed(false);
    try {
      const result = await getIndependentDiagnostic(role, id, request.signal);
      if (!request.signal.aborted) setDetail(result);
    } catch {
      if (!request.signal.aborted) setFailed(true);
    } finally {
      if (!request.signal.aborted) setBusy(false);
    }
  }
  return (
    <div>
      {!detail && (
        <button type="button" disabled={busy} onClick={() => void load()}>
          {synthetic
            ? words('Load safe diagnostic and source', '加载安全诊断与来源')
            : words('Load error details and source', '加载错误详情与来源')}
        </button>
      )}
      {failed && (
        <p role="alert">
          {words(
            'This diagnostic is unavailable or access has changed.',
            '诊断已不可用，或访问权限已变化。',
          )}
        </p>
      )}
      {detail && (
        <>
          {detail.body.save_state === 'saved' ? (
            <RawErrorViewer key={id} body={detail.body} />
          ) : (
            <OmittedBody state={detail.body.save_state} />
          )}
          {detail.source && (
            <section aria-label={words('Request source', '请求来源')}>
              <p>
                {words('Client headers are self-reported clues.', '客户端请求头是客户端自报线索。')}
              </p>
              <dl>
                {Object.entries(detail.source).map(([key, value]) => (
                  <div key={key}>
                    <dt>{key}</dt>
                    <dd>{typeof value === 'string' ? value : JSON.stringify(value)}</dd>
                  </div>
                ))}
              </dl>
            </section>
          )}
        </>
      )}
    </div>
  );
}
