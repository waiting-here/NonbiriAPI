import { useEffect, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { ErrorState, LoadingState } from '@shared/components/States';
import { TimeInput } from '@shared/components/TimeInput';
import { TimeContextNotice } from '@shared/components/TimeContext';
import { useActivityText } from '@shared/limitedactivities/copy';
import { responseOutcomeUnknown } from '@shared/operations/api';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { createTimeDraft, timeDraftValue } from '@shared/time';
import {
  changePeriodState, getNode, getPeriod, listPeriods, savePeriod, validatePeriod,
  type GraphValidation, type NodeRecord, type PeriodInput, type PeriodRecord,
} from './api';
import { NodeEditor } from './NodeEditor';
import { NodeMap } from './NodeMap';
import { useDraftGuard } from './useDraftGuard';
import { utf8Bytes } from './draft';

function GraphPreview({ graph, nodes }: { graph: GraphValidation; nodes: NodeRecord[] }) {
  const t = useActivityText();
  const name = (id: string) => nodes.find((node) => node.id === id)?.title ?? id;
  return <section className="fatfish-graph-preview" aria-label={t('发布前预览', 'Publish preview')}>
    <h3>{t('逻辑可达与试玩核验', 'Logical reachability and playtest proofs')}</h3>
    <p>{t('可发布', 'Publishable')}: {graph.publishable ? t('是', 'Yes') : t('否', 'No')}</p>
    <p>{t('可达节点', 'Reachable nodes')}: {graph.reachable.map(name).join('、') || '—'}</p>
    <p>{t('不可达节点', 'Unreachable nodes')}: {graph.unreachable.map(name).join('、') || '—'}</p>
    <p>{t('缺少至少一星试玩', 'Missing one-star playtest')}: {graph.missing_playtests.map(name).join('、') || '—'}</p>
  </section>;
}

function PeriodDraftEditor({ initial, onSaved, onDirty }: {
  initial: PeriodRecord | null; onSaved(period: PeriodRecord): void; onDirty(dirty: boolean): void;
}) {
  const t = useActivityText(), client = useQueryClient();
  const [title, setTitle] = useState(initial?.title ?? ''), [description, setDescription] = useState(initial?.description ?? '');
  const [visible, setVisible] = useState(initial?.visible ?? false), [paused, setPaused] = useState(initial?.paused ?? false);
  const [pastPublic, setPastPublic] = useState(initial?.past_public ?? false);
  const [start, setStart] = useState(() => createTimeDraft(initial?.starts_at ?? null, 'second'));
  const [end, setEnd] = useState(() => createTimeDraft(initial?.ends_at ?? null, 'second'));
  const [revision, setRevision] = useState(initial?.revision), [state, setState] = useState(initial?.state ?? 'draft');
  const [selectedNodeID, setSelectedNodeID] = useState<string | null>(null), [newNode, setNewNode] = useState(false);
  const [nodeDirty, setNodeDirty] = useState(false), [nodePosition, setNodePosition] = useState<{ id: string; x: number; y: number } | null>(null);
  const [error, setError] = useState<unknown>(null), [notice, setNotice] = useState('');
  const [graph, setGraph] = useState<GraphValidation | null>(null);
  const [saved, setSaved] = useState(() => JSON.stringify({ title: initial?.title ?? '', description: initial?.description ?? '', visible: initial?.visible ?? false,
    paused: initial?.paused ?? false, pastPublic: initial?.past_public ?? false, starts: initial?.starts_at ?? null, ends: initial?.ends_at ?? null }));
  const starts = timeDraftValue(start), ends = timeDraftValue(end);
  const current = JSON.stringify({ title, description, visible, paused, pastPublic,
    starts: start.text === start.originalText && !start.invalidInput ? start.originalEpoch : starts ?? start.text,
    ends: end.text === end.originalText && !end.invalidInput ? end.originalEpoch : ends ?? end.text });
  const dirty = saved !== current;
  const textValid = utf8Bytes(title) <= 128 && utf8Bytes(description) <= 8192;
  useDraftGuard(dirty || nodeDirty);
  useEffect(() => onDirty(dirty || nodeDirty), [dirty, nodeDirty, onDirty]);
  const save = useRetainedOperation((request: { id: string | null; input: PeriodInput }, key) => savePeriod(request.id, request.input, key),
    () => client.invalidateQueries({ queryKey: ['fatfish', 'periods'] }), ['admin', 'fatfish']);
  const transition = useRetainedOperation((request: { id: string; action: 'publish' | 'close' | 'reopen'; revision: string }, key) => changePeriodState(request.id, request.action, request.revision, key),
    () => Promise.all([client.invalidateQueries({ queryKey: ['fatfish', 'period', initial?.id] }),
      client.invalidateQueries({ queryKey: ['fatfish', 'periods'] })]), ['admin', 'fatfish']);
  const uncertain = save.isError && responseOutcomeUnknown(save.error);
  const editingLocked = save.isPending || uncertain;
  const period = initial ? { ...initial, revision: revision ?? initial.revision, state } : null;
  const submit = async () => {
    if (!title.trim() || !textValid || starts === null || starts === undefined || ends === null || ends === undefined || starts < 0 || ends <= starts || save.isPending) return;
    setError(null);
    const request = uncertain && save.variables ? save.variables : { id: initial?.id ?? null, input: { title, description, visible, paused, past_public: pastPublic,
      starts_at: starts, ends_at: ends, ...(revision ? { expected_revision: revision } : {}) } };
    try {
      const result = await save.mutateAsync(request);
      setSaved(JSON.stringify({ title: request.input.title, description: request.input.description, visible: request.input.visible,
        paused: request.input.paused, pastPublic: request.input.past_public, starts: request.input.starts_at, ends: request.input.ends_at }));
      setRevision(result.revision); setState(result.state); setNotice(t('期次草稿已保存。', 'Period draft saved.'));
      client.setQueryData(['fatfish', 'period', result.id], { ...result, nodes: initial?.nodes ?? [] });
      onSaved(result);
    } catch (cause) { setError(cause); }
  };
  const checkGraph = async () => {
    if (!period) return;
    setError(null);
    try { setGraph(await validatePeriod(period.id)); }
    catch (cause) { setError(cause); }
  };
  const changeState = async (action: 'publish' | 'close' | 'reopen') => {
    if (!period || !revision || dirty || nodeDirty || transition.isPending) return;
    setError(null);
    try {
      if (action === 'publish') {
        const latest = await validatePeriod(period.id);
        setGraph(latest);
        if (!latest.publishable) return;
      }
      const result = await transition.mutateAsync({ id: period.id, action, revision });
      setRevision(result.revision); setState(result.state); setNotice(`${t('期次状态', 'Period state')}: ${result.state}`);
      client.setQueryData(['fatfish', 'period', period.id], { ...result, nodes: period.nodes });
      onSaved(result);
    } catch (cause) { setError(cause); }
  };
  const selectNode = (id: string): boolean => {
    if (nodeDirty && !window.confirm(t('放弃未保存的节点修改？', 'Discard unsaved node changes?'))) return false;
    setSelectedNodeID(id); setNewNode(false); setNodeDirty(false); setNodePosition(null);
    return true;
  };
  const newNodeForm = () => {
    if (nodeDirty && !window.confirm(t('放弃未保存的节点修改？', 'Discard unsaved node changes?'))) return;
    setSelectedNodeID(null); setNewNode(true); setNodeDirty(false); setNodePosition(null);
  };
  const selectedDetail = useQuery({ queryKey: ['fatfish', 'node', period?.id, selectedNodeID], queryFn: () => getNode(period!.id, selectedNodeID!), enabled: !!period && !!selectedNodeID });
  return <div className="fatfish-period-editor">
    <h2>{initial ? t('编辑期次', 'Edit period') : t('新建期次', 'New period')}</h2>
    {period ? <p role="status">{t('当前状态', 'Current state')}: {state} · r{revision}</p> : null}
    <div className="fatfish-fields" inert={editingLocked}>
      <label>{t('标题', 'Title')}<input value={title} maxLength={128} onChange={(event) => setTitle(event.target.value)} /></label>
      <label>{t('说明', 'Description')}<textarea value={description} maxLength={8192} onChange={(event) => setDescription(event.target.value)} /></label>
      <label><input type="checkbox" checked={visible} onChange={(event) => setVisible(event.target.checked)} />{t('目录可见', 'Visible in directory')}</label>
      <label><input type="checkbox" checked={paused} onChange={(event) => setPaused(event.target.checked)} />{t('暂停挑战', 'Pause challenges')}</label>
      <label><input type="checkbox" checked={pastPublic} onChange={(event) => setPastPublic(event.target.checked)} />{t('结束后公开', 'Public after close')}</label>
    </div>
    <TimeContextNotice station="admin" />
    <div className="fatfish-fields" inert={editingLocked}><TimeInput label={t('开始时间', 'Start time')} station="admin" draft={start} showZoneHint={false} onChange={setStart} />
      <TimeInput label={t('结束时间（不含）', 'End time (exclusive)')} station="admin" draft={end} showZoneHint={false} onChange={setEnd} /></div>
    <p>{t('保存草稿不会自动开放正式期次；发布前要检查逻辑可达性与试玩证据。', 'Saving a draft never opens a formal period; check reachability and playtest proofs before publishing.')}</p>
    {dirty ? <p role="status">{t('有未保存修改。', 'Unsaved changes.')}</p> : null}
    {!textValid ? <p role="alert">{t('标题最多128字节，说明最多8192字节（按UTF-8计算）。', 'Title is limited to 128 UTF-8 bytes; description to 8192 bytes.')}</p> : null}
    {uncertain ? <p role="status">{t('保存结果未确定，请重试同一请求。', 'Save outcome unknown. Retry the same request.')}</p> : null}
    <div className="fatfish-actions"><button type="button" disabled={!title.trim() || !textValid || starts === null || starts === undefined || ends === null || ends === undefined || ends <= starts || save.isPending} onClick={() => void submit()}>{uncertain ? t('重试保存', 'Retry save') : t('保存期次', 'Save period')}</button>
      {period ? <button type="button" disabled={editingLocked} onClick={() => void checkGraph()}>{t('检查并预览发布条件', 'Check and preview publish conditions')}</button> : null}
      {period && state === 'draft' ? <button type="button" disabled={editingLocked || dirty || nodeDirty || transition.isPending} onClick={() => void changeState('publish')}>{t('发布期次', 'Publish period')}</button> : null}
      {period && state === 'open' ? <button type="button" disabled={editingLocked || dirty || nodeDirty || transition.isPending} onClick={() => void changeState('close')}>{t('关闭期次', 'Close period')}</button> : null}
      {period && state === 'closed' ? <button type="button" disabled={editingLocked || dirty || nodeDirty || transition.isPending} onClick={() => void changeState('reopen')}>{t('重新开放', 'Reopen period')}</button> : null}
    </div>
    {graph && period ? <GraphPreview graph={graph} nodes={period.nodes ?? []} /> : null}
    {notice ? <p role="status">{notice}</p> : null}
    {error ? <ErrorState error={error} /> : null}
    {period ? <div inert={editingLocked}>
      <div className="fatfish-actions"><h3>{t('节点编排', 'Node arrangement')}</h3>
        <button type="button" disabled={state === 'closed' || (period.nodes?.length ?? 0) >= 128} onClick={newNodeForm}>{t('新增节点', 'Add node')}</button></div>
      <NodeMap nodes={period.nodes ?? []} selectedID={selectedNodeID} selectedCondition={selectedDetail.data?.condition}
        onSelect={selectNode} onMove={(id, x, y) => { if (selectNode(id)) setNodePosition({ id, x, y }); }} />
      {selectedNodeID || newNode ? <NodeEditor period={period} nodeID={selectedNodeID} position={nodePosition} onDirty={setNodeDirty}
        onSaved={(node) => {
          setSelectedNodeID(node.id); setNewNode(false); setNodeDirty(false); setGraph(null); setNodePosition(null);
          void getPeriod(period.id).then((latest) => { client.setQueryData(['fatfish', 'period', period.id], latest); setRevision(latest.revision); }).catch(setError);
        }} /> : null}
    </div> : null}
  </div>;
}

export function PeriodManager() {
  const t = useActivityText();
  const [page, setPage] = useState(1), [selectedID, setSelectedID] = useState<string | null>(null), [generation, setGeneration] = useState(0);
  const [draftDirty, setDraftDirty] = useState(false);
  const list = useQuery({ queryKey: ['fatfish', 'periods', page], queryFn: () => listPeriods(page) });
  const detail = useQuery({ queryKey: ['fatfish', 'period', selectedID], queryFn: () => getPeriod(selectedID!), enabled: !!selectedID });
  const switchTo = (id: string | null) => {
    if (draftDirty && !window.confirm(t('放弃当前未保存期次或节点？', 'Discard unsaved period or node changes?'))) return;
    setSelectedID(id); setGeneration((value) => value + 1); setDraftDirty(false);
  };
  return <div className="fatfish-manager">
    <aside className="fatfish-directory"><h2>{t('期次目录', 'Period directory')}</h2>
      <button type="button" onClick={() => switchTo(null)}>{t('新建期次', 'New period')}</button>
      {list.isPending ? <LoadingState /> : list.error ? <ErrorState error={list.error} onRetry={() => void list.refetch()} /> : <>
        <ul>{list.data?.items.map((item) => <li key={item.id}><button type="button" onClick={() => switchTo(item.id)}>{item.title} · {item.state}</button></li>)}</ul>
        <button type="button" disabled={page <= 1} onClick={() => setPage(page - 1)}>{t('上一页', 'Previous')}</button>
        <button type="button" disabled={!list.data?.has_more} onClick={() => setPage(page + 1)}>{t('下一页', 'Next')}</button>
      </>}
    </aside>
    {selectedID && detail.isPending ? <LoadingState /> : selectedID && detail.error ? <ErrorState error={detail.error} onRetry={() => void detail.refetch()} /> :
      <PeriodDraftEditor key={`${selectedID ?? 'new'}-${generation}`} initial={selectedID ? detail.data ?? null : null}
        onDirty={setDraftDirty} onSaved={(item) => { setSelectedID(item.id); setDraftDirty(false); }} />}
  </div>;
}
