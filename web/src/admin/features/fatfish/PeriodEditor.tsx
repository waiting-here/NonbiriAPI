import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { ErrorState, LoadingState } from '@shared/components/States';
import { TimeInput } from '@shared/components/TimeInput';
import { TimeContextNotice } from '@shared/components/TimeContext';
import { useFatFishText } from './copy';
import { responseOutcomeUnknown } from '@shared/operations/api';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { createTimeDraft, timeDraftValue } from '@shared/time';
import {
  changePeriodState, getPeriod, listPeriods, savePeriod, validatePeriod,
  type Condition, type GraphValidation, type NodeRecord, type PeriodInput, type PeriodRecord,
} from './api';
import { NodeEditor } from './NodeEditor';
import { PeriodGraph } from './PeriodGraph';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { addPassedPrerequisite, EmptyConditionGroup, removeConditionEdge, type ConditionPath } from './conditionEdges';
import { useDraftGuard, useWorkspaceConfirm } from './useDraftGuard';
import { utf8Bytes } from './draft';

function GraphPreview({ graph, nodes }: { graph: GraphValidation; nodes: NodeRecord[] }) {
  const text = useFatFishText();
  const name = (id: string) => nodes.find((node) => node.id === id)?.title ?? id;
  return <section className="fatfish-graph-preview" aria-label={text('publish_preview')}>
    <h3>{text('logical_reachability_and_playtest_proofs')}</h3>
    <p>{text('publishable')}: {graph.publishable ? text('yes') : text('no_2')}</p>
    <p>{text('reachable_nodes')}: {graph.reachable.map(name).join('、') || '—'}</p>
    <p>{text('unreachable_nodes')}: {graph.unreachable.map(name).join('、') || '—'}</p>
    <p>{text('missing_one_star_playtest')}: {graph.missing_playtests.map(name).join('、') || '—'}</p>
  </section>;
}

function PeriodDraftEditor({ initial, onSaved, onDirty }: {
  initial: PeriodRecord | null; onSaved(period: PeriodRecord): void; onDirty(dirty: boolean): void;
}) {
  const text = useFatFishText(), client = useQueryClient();
  const { t } = useTranslation();
  const [confirmAction, setConfirmAction] = useState<'publish' | 'close' | 'reopen' | null>(null);
  const [title, setTitle] = useState(initial?.title ?? ''), [description, setDescription] = useState(initial?.description ?? '');
  const [visible, setVisible] = useState(initial?.visible ?? false), [paused, setPaused] = useState(initial?.paused ?? false);
  const [pastPublic, setPastPublic] = useState(initial?.past_public ?? false);
  const [start, setStart] = useState(() => createTimeDraft(initial?.starts_at ?? null, 'second'));
  const [end, setEnd] = useState(() => createTimeDraft(initial?.ends_at ?? null, 'second'));
  const [revision, setRevision] = useState(initial?.revision), [state, setState] = useState(initial?.state ?? 'draft');
  const [selectedNodeID, setSelectedNodeID] = useState<string | null>(null), [newNode, setNewNode] = useState(false);
  const [nodeDirty, setNodeDirty] = useState(false);
  const [selectedCondition, setSelectedCondition] = useState<Condition | undefined>();
  const [emptyEdge, setEmptyEdge] = useState<{ target: string; value: Condition; path: ConditionPath } | null>(null);
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
  const { dialog: leaveDialog } = useDraftGuard(dirty || nodeDirty);
  const changingNode = useWorkspaceConfirm();
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
      setRevision(result.revision); setState(result.state); setNotice(text('period_draft_saved'));
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
      setRevision(result.revision); setState(result.state); setNotice(`${text('period_state')}: ${result.state}`);
      client.setQueryData(['fatfish', 'period', period.id], { ...result, nodes: period.nodes });
      onSaved(result);
    } catch (cause) { setError(cause); }
  };
  const selectNode = async (id: string): Promise<boolean> => {
    if (id === selectedNodeID && !newNode) return true;
    if (nodeDirty && !await changingNode.confirm(text('discard_unsaved_node_changes'))) return false;
    setSelectedNodeID(id); setNewNode(false); setNodeDirty(false); setSelectedCondition(period?.nodes?.find((node) => node.id === id)?.condition);
    return true;
  };
  const newNodeForm = async () => {
    if (nodeDirty && !await changingNode.confirm(text('discard_unsaved_node_changes'))) return;
    setSelectedNodeID(null); setNewNode(true); setNodeDirty(false);
  };
  const applyCondition = async (id: string, value: Condition) => {
    if (id !== selectedNodeID && !await selectNode(id)) return;
    setSelectedCondition(value);
  };
  const graphCondition = (id: string) => id === selectedNodeID && selectedCondition ? selectedCondition : period?.nodes?.find((node) => node.id === id)?.condition ?? {};
  const removeEdge = (target: string, path: ConditionPath) => {
    const value = graphCondition(target);
    try { applyCondition(target, removeConditionEdge(value, path)); }
    catch (cause) {
      if (cause instanceof EmptyConditionGroup) setEmptyEdge({ target, value, path });
      else setError(cause);
    }
  };
  return <div className="fatfish-period-editor">{leaveDialog}{changingNode.dialog}
    <h2>{initial ? text('edit_period') : text('new_period')}</h2>
    {period ? <p role="status">{text('current_state')}: {state}</p> : null}
    <div className="fatfish-fields" inert={editingLocked} onChange={() => setNotice('')}>
      <label>{text('title')}<input value={title} maxLength={128} onChange={(event) => setTitle(event.target.value)} /></label>
      <label>{text('description')}<textarea value={description} maxLength={8192} onChange={(event) => setDescription(event.target.value)} /></label>
      <label><input type="checkbox" checked={visible} onChange={(event) => setVisible(event.target.checked)} />{text('visible_in_directory')}</label>
      <label><input type="checkbox" checked={paused} onChange={(event) => setPaused(event.target.checked)} />{text('pause_challenges')}</label>
      <label><input type="checkbox" checked={pastPublic} onChange={(event) => setPastPublic(event.target.checked)} />{text('public_after_close')}</label>
    </div>
    <TimeContextNotice station="admin" />
    <div className="fatfish-fields" inert={editingLocked}><TimeInput label={text('start_time')} station="admin" draft={start} showZoneHint={false} onChange={(value) => { setNotice(''); setStart(value); }} />
      <TimeInput label={text('end_time_exclusive')} station="admin" draft={end} showZoneHint={false} onChange={(value) => { setNotice(''); setEnd(value); }} /></div>
    <p>{text('saving_a_draft_never_opens_a_formal_period_check_reachability_and_play')}</p>
    {dirty ? <p role="status">{text('unsaved_changes')}</p> : null}
    {!textValid ? <p role="alert">{text('title_is_limited_to_128_utf_8_bytes_description_to_8192_bytes')}</p> : null}
    {uncertain ? <p role="status">{text('save_outcome_unknown_retry_the_same_request')}</p> : null}
    <div className="fatfish-actions"><button type="button" disabled={!title.trim() || !textValid || starts === null || starts === undefined || ends === null || ends === undefined || ends <= starts || save.isPending} onClick={() => void submit()}>{uncertain ? text('retry_save') : text('save_period')}</button>
      {period ? <button type="button" disabled={editingLocked} onClick={() => void checkGraph()}>{text('check_and_preview_publish_conditions')}</button> : null}
      {period && state === 'draft' ? <button type="button" disabled={editingLocked || dirty || nodeDirty || transition.isPending} onClick={() => setConfirmAction('publish')}>{text('publish_period')}</button> : null}
      {period && state === 'open' ? <button type="button" disabled={editingLocked || dirty || nodeDirty || transition.isPending} onClick={() => setConfirmAction('close')}>{text('close_period')}</button> : null}
      {period && state === 'closed' ? <button type="button" disabled={editingLocked || dirty || nodeDirty || transition.isPending} onClick={() => setConfirmAction('reopen')}>{text('reopen_period')}</button> : null}
    </div>
    <ConfirmDialog open={confirmAction !== null} title={period?.title ?? t('common.operations.management.periodActions')}
      description={t('common.operations.management.periodActionHelp', { action: confirmAction === 'publish' ? text('publish_period') : confirmAction === 'close' ? text('close_period') : text('reopen_period') })}
      confirmLabel={confirmAction === 'publish' ? text('publish_period') : confirmAction === 'close' ? text('close_period') : text('reopen_period')}
      danger={confirmAction === 'close'} busy={transition.isPending} onCancel={() => setConfirmAction(null)} onConfirm={() => {
        if (confirmAction) { void changeState(confirmAction); setConfirmAction(null); }
      }} />
    {graph && period ? <GraphPreview graph={graph} nodes={period.nodes ?? []} /> : null}
    {notice ? <p role="status">{notice}</p> : null}
    {error ? <ErrorState error={error} /> : null}
    {period ? <div inert={editingLocked}>
      <div className="fatfish-actions"><h3>{text('node_arrangement')}</h3>
        <button type="button" disabled={state === 'closed' || (period.nodes?.length ?? 0) >= 128} onClick={newNodeForm}>{text('add_node')}</button></div>
      <PeriodGraph period={period} selectedID={selectedNodeID} selectedCondition={selectedCondition}
        onSelect={selectNode} onConnect={(source, target) => applyCondition(target, addPassedPrerequisite(graphCondition(target), source))} onRemove={removeEdge} />
      {selectedNodeID || newNode ? <NodeEditor period={period} nodeID={selectedNodeID} conditionValue={selectedCondition} onCondition={setSelectedCondition} onDirty={setNodeDirty}
        onSaved={(node) => {
          setSelectedNodeID(node.id); setNewNode(false); setNodeDirty(false); setGraph(null);
          void getPeriod(period.id).then((latest) => { client.setQueryData(['fatfish', 'period', period.id], latest); setRevision(latest.revision); }).catch(setError);
        }} /> : null}
    </div> : null}
    <ConfirmDialog open={!!emptyEdge} title={text('the_condition_group_would_be_empty')}
      description={text('choose_to_remove_the_empty_group_or_make_it_always_true_other_conditio')}
      confirmLabel={text('remove_empty_group')} onCancel={() => setEmptyEdge(null)} onConfirm={() => {
        if (emptyEdge) applyCondition(emptyEdge.target, removeConditionEdge(emptyEdge.value, emptyEdge.path, 'remove_group'));
        setEmptyEdge(null);
      }}>
      <button type="button" onClick={() => {
        if (emptyEdge) applyCondition(emptyEdge.target, removeConditionEdge(emptyEdge.value, emptyEdge.path, 'always'));
        setEmptyEdge(null);
      }}>{text('make_always_true')}</button>
    </ConfirmDialog>
  </div>;
}

export function PeriodManager() {
  const text = useFatFishText();
  const [page, setPage] = useState(1), [selectedID, setSelectedID] = useState<string | null>(null), [generation, setGeneration] = useState(0);
  const [draftDirty, setDraftDirty] = useState(false);
  const list = useQuery({ queryKey: ['fatfish', 'periods', page], queryFn: () => listPeriods(page) });
  const detail = useQuery({ queryKey: ['fatfish', 'period', selectedID], queryFn: () => getPeriod(selectedID!), enabled: !!selectedID });
  const switching = useWorkspaceConfirm();
  const switchTo = async (id: string | null) => {
    if (draftDirty && !await switching.confirm(text('discard_unsaved_period_or_node_changes'))) return;
    setSelectedID(id); setGeneration((value) => value + 1); setDraftDirty(false);
  };
  return <div className="fatfish-manager">{switching.dialog}
    <aside className="fatfish-directory"><h2>{text('period_directory')}</h2>
      <button type="button" onClick={() => switchTo(null)}>{text('new_period')}</button>
      {list.isPending ? <LoadingState /> : list.error ? <ErrorState error={list.error} onRetry={() => void list.refetch()} /> : <>
        <ul>{list.data?.items.map((item) => <li key={item.id}><button type="button" onClick={() => switchTo(item.id)}>{item.title} · {item.state}</button></li>)}</ul>
        <button type="button" disabled={page <= 1} onClick={() => setPage(page - 1)}>{text('previous')}</button>
        <button type="button" disabled={!list.data?.has_more} onClick={() => setPage(page + 1)}>{text('next')}</button>
      </>}
    </aside>
    {selectedID && detail.isPending ? <LoadingState /> : selectedID && detail.error ? <ErrorState error={detail.error} onRetry={() => void detail.refetch()} /> :
      <PeriodDraftEditor key={`${selectedID ?? 'new'}-${generation}`} initial={selectedID ? detail.data ?? null : null}
        onDirty={setDraftDirty} onSaved={(item) => { setSelectedID(item.id); setDraftDirty(false); }} />}
  </div>;
}
