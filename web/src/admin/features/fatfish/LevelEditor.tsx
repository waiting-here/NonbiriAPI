import { useEffect, useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { ErrorState, LoadingState } from '@shared/components/States';
import { useActivityText } from '@shared/limitedactivities/copy';
import { operationKey, responseOutcomeUnknown } from '@shared/operations/api';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { FISH_RADIUS, type Level, type Point, type Polygon } from '@shared/fatfish/engine/types';
import { formatScoreUnits } from '@shared/fatfish/api';
import { toolIcon, toolNames } from '@shared/fatfish/art';
import { laidOutLevel } from '@shared/fatfish/workspace';
import {
  deleteLevel, getLevel, getVersion, listLevels, listPlaytests, listVersions, publishVersion,
  saveLevel, validateLevelOnServer, type LevelInput, type LevelRecord,
  type VersionRecord,
} from './api';
import { LevelCanvas } from './LevelCanvas';
import { ShapeInspector } from './ShapeInspector';
import { ExamplePicker } from './ExamplePicker';
import { PlaytestPane } from './PlaytestPane';
import { blankLevel, cloneLevel, importDraft, localValidation, nextID, rect, snap, toolPolygon, utf8Bytes, type ObjectKind, type Selection, unit } from './draft';
import { useHistory } from './history';
import { useDraftGuard } from './useDraftGuard';

function addObject(level: Level, kind: ObjectKind, resourceKey = 'barrier'): { level: Level; selection: Selection } {
  const copy = cloneLevel(level), id = nextID(copy);
  const polygon: Polygon = kind === 'tools' ? toolPolygon(resourceKey) : rect(unit(240), unit(280), unit(48), unit(40));
  switch (kind) {
    case 'fish': copy.fish.push({ id, x: unit(100), y: unit(440), heading: 0 }); break;
    case 'tools': copy.tools.push({ id, polygon, resource_key: resourceKey, placed: false, x: unit(240), y: unit(280) }); break;
    case 'solids': copy.solids.push({ id, polygon }); break;
    case 'hazards': copy.hazards.push({ id, polygon }); break;
    case 'bowls': copy.bowls.push({ id, polygon, required: 0, capacity: Math.max(1, copy.fish.length) }); break;
    case 'switches': copy.switches.push({ id, polygon, mode: 'latch' }); break;
    case 'gates': copy.gates.push({ id, polygon, mode: 'any', initially_open: false, switch_ids: [] }); break;
    case 'directions': copy.directions.push({ id, polygon, mode: 'entry', heading: 0 }); break;
  }
  return { level: laidOutLevel(copy), selection: { kind, id } };
}
function removeObject(level: Level, selection: Selection): Level {
  const copy = cloneLevel(level);
  switch (selection.kind) {
    case 'fish': copy.fish = copy.fish.filter((item) => item.id !== selection.id); break;
    case 'tools': copy.tools = copy.tools.filter((item) => item.id !== selection.id); break;
    case 'solids': copy.solids = copy.solids.filter((item) => item.id !== selection.id); break;
    case 'hazards': copy.hazards = copy.hazards.filter((item) => item.id !== selection.id); break;
    case 'bowls': copy.bowls = copy.bowls.filter((item) => item.id !== selection.id); break;
    case 'switches': copy.switches = copy.switches.filter((item) => item.id !== selection.id); copy.gates.forEach((gate) => { gate.switch_ids = gate.switch_ids.filter((id) => id !== selection.id); }); break;
    case 'gates': copy.gates = copy.gates.filter((item) => item.id !== selection.id); break;
    case 'directions': copy.directions = copy.directions.filter((item) => item.id !== selection.id); break;
  }
  return copy;
}
const limits: Record<ObjectKind, number> = { fish: 40, tools: 24, solids: 24, hazards: 24, bowls: 8, switches: 16, gates: 16, directions: 24 };
const objectKinds: ObjectKind[] = ['fish', 'solids', 'hazards', 'bowls', 'switches', 'gates', 'directions'];
const label = (kind: ObjectKind, zh: boolean) => ({
  fish: zh ? '小鱼' : 'Fish', tools: zh ? '可移动工具' : 'Movable tool', solids: zh ? '固定障碍' : 'Fixed obstacle',
  hazards: zh ? '危险区' : 'Hazard', bowls: zh ? '碗' : 'Bowl', switches: zh ? '开关' : 'Switch',
  gates: zh ? '门' : 'Gate', directions: zh ? '方向区' : 'Direction zone',
})[kind];

function download(name: string, value: unknown) {
  const blob = new Blob([JSON.stringify(value, null, 2) + '\n'], { type: 'application/json' });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement('a');
  anchor.href = url; anchor.download = name; document.body.append(anchor); anchor.click(); anchor.remove();
  setTimeout(() => URL.revokeObjectURL(url), 0);
}

function VersionShelf({ levelID }: { levelID: string }) {
  const t = useActivityText();
  const [page, setPage] = useState(1), [versionID, setVersionID] = useState<string | null>(null);
  const versions = useQuery({ queryKey: ['fatfish', 'versions', levelID, page], queryFn: () => listVersions(levelID, page) });
  const version = useQuery({ queryKey: ['fatfish', 'version', versionID], queryFn: () => getVersion(versionID!), enabled: !!versionID });
  const proofs = useQuery({ queryKey: ['fatfish', 'playtests', versionID], queryFn: () => listPlaytests(versionID!), enabled: !!versionID });
  return <section className="fatfish-shelf">
    <h3>{t('不可变版本与试玩核验', 'Immutable versions and playtest proofs')}</h3>
    {versions.isPending ? <LoadingState /> : versions.error ? <ErrorState error={versions.error} /> : <>
      <ul>{versions.data?.items.map((item) => <li key={item.id}>
        <button type="button" onClick={() => setVersionID(item.id)}>{item.id} · {item.content_hash.slice(0, 12)}</button>
      </li>)}</ul>
      <button type="button" disabled={page <= 1} onClick={() => setPage(page - 1)}>{t('上一页', 'Previous')}</button>
      <button type="button" disabled={!versions.data?.has_more} onClick={() => setPage(page + 1)}>{t('下一页', 'Next')}</button>
    </>}
    {versionID ? <div>
      {version.isPending ? <LoadingState /> : version.error ? <ErrorState error={version.error} /> : <p>{t('版本哈希', 'Version hash')}: <code>{version.data?.content_hash}</code></p>}
      {version.data ? <PlaytestPane key={versionID} versionID={versionID} contentHash={version.data.content_hash} /> : null}
      {proofs.isPending ? <LoadingState /> : proofs.error ? <ErrorState error={proofs.error} /> : <>
        <p>{t('最近记录中的至少一星核验证据', 'Verified one-star proof in recent records')}: {proofs.data?.some((item) => item.passed && item.stars >= 1) ? t('已有', 'Available') : t('最近记录中未显示；发布检查以服务端完整记录为准', 'Not shown recently; the server checks the full record before publication')}</p>
        <ul>{proofs.data?.map((item) => <li key={item.id}>{item.id} · {item.stars}★ · {formatScoreUnits(item.score_units)}</li>)}</ul>
      </>}
    </div> : null}
  </section>;
}

function LevelDraftEditor({ initial, onSaved, onDirty, onDeleted }: { initial: LevelRecord | null; onSaved(record: LevelRecord): void; onDirty(dirty: boolean): void; onDeleted(): void }) {
  const t = useActivityText(), client = useQueryClient();
  const baseline = initial?.draft ?? blankLevel();
  const history = useHistory<Level>(baseline);
  const level = history.state.present;
  const visualLevel = useMemo(() => laidOutLevel(level), [level]);
  const [title, setTitle] = useState(initial?.title ?? ''), [description, setDescription] = useState(initial?.description ?? '');
  const [revision, setRevision] = useState(initial?.revision), [selected, setSelected] = useState<Selection | null>(null);
  const [grid, setGrid] = useState(true), [placingFish, setPlacingFish] = useState(false), [fishHeading, setFishHeading] = useState(0);
  const [requiredPlacement, setRequiredPlacement] = useState(0), [notice, setNotice] = useState('');
  const [error, setError] = useState<unknown>(null), [serverValidation, setServerValidation] = useState<VersionRecord | null>(null);
  const [savedSnapshot, setSavedSnapshot] = useState(() => JSON.stringify({ title: initial?.title ?? '', description: initial?.description ?? '', level: baseline }));
  const dirty = savedSnapshot !== JSON.stringify({ title, description, level });
  const invalid = useMemo(() => localValidation(level), [level]);
  const textValid = utf8Bytes(title) <= 128 && utf8Bytes(description) <= 4096;
  const save = useRetainedOperation((request: { id: string | null; input: LevelInput }, key) => saveLevel(request.id, request.input, key),
    () => client.invalidateQueries({ queryKey: ['fatfish', 'levels'] }), ['admin', 'fatfish']);
  const publish = useRetainedOperation((request: { id: string; revision: string }, key) => publishVersion(request.id, request.revision, key),
    () => client.invalidateQueries({ queryKey: ['fatfish', 'versions'] }), ['admin', 'fatfish']);
  const deletion = useRetainedOperation((request: { id: string; revision: string }, key) => deleteLevel(request.id, request.revision, key),
    () => client.invalidateQueries({ queryKey: ['fatfish', 'levels'] }), ['admin', 'fatfish']);
  const deleteUncertain = deletion.isError && responseOutcomeUnknown(deletion.error);
  const uncertain = save.isError && responseOutcomeUnknown(save.error);
  const editingLocked = save.isPending || uncertain || deletion.isPending || deleteUncertain;
  const mayReplace = useDraftGuard(dirty || editingLocked);
  useEffect(() => onDirty(dirty || editingLocked), [dirty, editingLocked, onDirty]);
  const commit = (next: Level) => { if (editingLocked) return; history.commit(next); setServerValidation(null); setError(null); };
  const undo = () => { if (!editingLocked) { history.undo(); setServerValidation(null); setError(null); } };
  const redo = () => { if (!editingLocked) { history.redo(); setServerValidation(null); setError(null); } };
  const removeSelected = () => { if (selected) { commit(removeObject(level, selected)); setSelected(null); } };
  const placeFish = (position: Point) => {
    if (level.fish.length >= limits.fish) return;
    const next = cloneLevel(level), id = nextID(next);
    next.fish.push({ id, x: snap(Math.max(FISH_RADIUS, Math.min(unit(480) - FISH_RADIUS, position.x)), grid),
      y: snap(Math.max(FISH_RADIUS, Math.min(unit(560) - FISH_RADIUS, position.y)), grid), heading: fishHeading });
    commit(next); setSelected({ kind: 'fish', id }); setPlacingFish(false);
  };
  const submit = async () => {
    if (invalid || !title.trim() || !textValid || requiredPlacement > level.fish.length || save.isPending) return;
    setError(null);
    const request = uncertain && save.variables ? save.variables : { id: initial?.id ?? null, input: { title, description, draft: level, ...(revision ? { expected_revision: revision } : {}) } };
    try {
      const result = await save.mutateAsync(request);
      const clean = JSON.stringify({ title: request.input.title, description: request.input.description, level: request.input.draft });
      setSavedSnapshot(clean); setRevision(result.revision); setNotice(t('草稿已保存。', 'Draft saved.'));
      client.setQueryData(['fatfish', 'level', result.id], { ...result, draft: request.input.draft });
      onSaved(result);
    } catch (cause) { setError(cause); }
  };
  const validate = async () => {
    if (invalid) return;
    setError(null);
    try { setServerValidation(await validateLevelOnServer(level, operationKey())); }
    catch (cause) { setError(cause); }
  };
  const publishCurrent = async () => {
    if (!initial?.id || !revision || dirty || publish.isPending || editingLocked) return;
    setError(null);
    try { const value = await publish.mutateAsync({ id: initial.id, revision }); setNotice(`${t('版本已发布', 'Version published')}: ${value.id} · ${value.content_hash}`); }
    catch (cause) { setError(cause); }
  };
  const deleteCurrent = async () => {
    if (!initial?.id || !revision || deletion.isPending || save.isPending || uncertain || publish.isPending) return;
    if (!deleteUncertain && !window.confirm(t(
      '删除这个关卡？它将从编辑库移除，未保存的编辑也会丢弃。已有活动、游玩记录和成绩仍保留使用的版本。',
      'Delete this level from the library and discard unsaved edits? Existing activities, plays and scores keep their published version.',
    ))) return;
    setError(null);
    try {
      await deletion.mutateAsync(deleteUncertain && deletion.variables ? deletion.variables : { id: initial.id, revision });
      client.removeQueries({ queryKey: ['fatfish', 'level', initial.id], exact: true });
      onDirty(false); onDeleted();
    } catch (cause) { setError(cause); }
  };
  const importFile = async (file: File | undefined) => {
    if (!file) return;
    if (!mayReplace(t('放弃现有未保存草稿并导入？', 'Discard unsaved draft and import?'))) return;
    setError(null);
    try {
      if (file.size > 256 * 1024) throw new Error('Import exceeds 256 KiB');
      const converted = importDraft(await file.text());
      history.commit(converted.level); setTitle(converted.title); setDescription(converted.description);
      setRequiredPlacement(converted.pendingFishCount); setNotice(converted.warning ?? t('导入成功，发布前仍须重新验证。', 'Imported. Revalidate before publishing.'));
      setServerValidation(null);
    } catch (cause) { setError(cause); }
  };
  const selectError = () => {
    const match = invalid?.match(/(fish|tools|solids|hazards|bowls|switches|gates|directions)\[(\d+)\]/);
    if (!match) return;
    const kind = match[1] as ObjectKind, index = Number(match[2]);
    const item = level[kind][index];
    if (item) setSelected({ kind, id: item.id });
  };
  return <div className="fatfish-editor">
    <section className="fatfish-toolbar">
      <h2>{initial ? t('编辑关卡草稿', 'Edit level draft') : t('新建关卡草稿', 'New level draft')}</h2>
      <div className="fatfish-fields" inert={editingLocked}>
        <label>{t('标题', 'Title')}<input value={title} maxLength={128} onChange={(event) => setTitle(event.target.value)} /></label>
        <label>{t('说明', 'Description')}<textarea value={description} maxLength={4096} onChange={(event) => setDescription(event.target.value)} /></label>
      </div>
      <details className="fatfish-level-settings" inert={editingLocked}><summary>{t('时长、速度与星级门槛', 'Duration, speed and star thresholds')}</summary><div className="fatfish-fields">
        <label>{t('时长（秒）', 'Duration (seconds)')}<input type="number" min={10} max={600} value={level.duration_seconds} onChange={(event) => { const next = cloneLevel(level); next.duration_seconds = Number(event.target.value); commit(next); }} /></label>
        <label>{t('速度（像素/秒）', 'Speed (pixels/second)')}<input type="number" min={16} max={160} value={level.speed_pixels_per_second} onChange={(event) => { const next = cloneLevel(level); next.speed_pixels_per_second = Number(event.target.value); commit(next); }} /></label>
        {level.thresholds.map((value, index) => <label key={index}>{index + 1}★ {t('所需小鱼', 'fish required')}<input type="number" min={1} max={level.fish.length} value={value} onChange={(event) => { const next = cloneLevel(level); next.thresholds[index] = Number(event.target.value); commit(next); }} /></label>)}
      </div></details>
      <div className="fatfish-actions" inert={editingLocked}><label><input type="checkbox" checked={grid} onChange={(event) => setGrid(event.target.checked)} />{t('8像素网格吸附', 'Snap to 8-pixel grid')}</label>
        <button type="button" disabled={!history.state.past.length} onClick={undo}>{t('撤销', 'Undo')}</button>
        <button type="button" disabled={!history.state.future.length} onClick={redo}>{t('重做', 'Redo')}</button>
        <label>{t('新鱼朝向', 'New fish heading')}<input type="number" min={0} max={4095} value={fishHeading} onChange={(event) => setFishHeading(Number(event.target.value))} /></label>
      </div>
      <div className="fatfish-actions" inert={editingLocked}>{objectKinds.map((kind) => <button key={kind} type="button" disabled={level[kind].length >= limits[kind]}
        onClick={() => {
          if (kind === 'fish') { setPlacingFish(true); return; }
          const next = addObject(level, kind); commit(next.level); setSelected(next.selection);
        }}>{t('新增', 'Add')} {label(kind, t('是', 'no') === '是')}</button>)}</div>
      <div className="fatfish-tool-palette" inert={editingLocked} aria-label={t('新增可移动道具', 'Add movable pieces')}>
        {Object.entries(toolNames).map(([key, name]) => <button key={key} type="button" disabled={level.tools.length >= limits.tools} onClick={() => {
          const next = addObject(level, 'tools', key); commit(next.level); setSelected(next.selection);
        }}><img src={`/assets/fatfish/svg/${toolIcon(key)}.svg`} alt="" />{t(name[0], name[1])}</button>)}
      </div>
      {placingFish ? <p role="status">{t('点击地图放置下一条鱼。', 'Click the map to place the next fish.')}</p> : null}
      {requiredPlacement > 0 ? <p role="status">{t('旧关卡需逐鱼布点', 'Legacy fish must be placed individually')}: {level.fish.length}/{requiredPlacement}. {t('旧通过记录不计入核验。', 'Old passed results do not count as proof.')}</p> : null}
      <div className="fatfish-actions">
        <label className="fatfish-file" inert={editingLocked}>{t('导入 JSON／v0.6 转换', 'Import JSON / convert v0.6')}<input type="file" accept=".json,application/json" disabled={editingLocked} onChange={(event) => { void importFile(event.target.files?.[0]); event.target.value = ''; }} /></label>
        <button type="button" disabled={editingLocked} onClick={() => download(`${title.trim() || 'fatfish-level'}.json`, { title, description, draft: level })}>{t('导出草稿', 'Export draft')}</button>
        <button type="button" disabled={editingLocked || !!invalid} onClick={() => void validate()}>{t('服务端校验', 'Validate on server')}</button>
        <button type="button" disabled={!!invalid || !title.trim() || !textValid || requiredPlacement > level.fish.length || save.isPending || deletion.isPending || deleteUncertain || (uncertain && !save.variables)} onClick={() => void submit()}>{uncertain ? t('重试同一保存', 'Retry same save') : t('保存草稿', 'Save draft')}</button>
        {initial?.id ? <>
          <button type="button" disabled={dirty || !!invalid || publish.isPending || editingLocked} onClick={() => void publishCurrent()}>{t('从已保存草稿发布不可变版本', 'Publish immutable version from saved draft')}</button>
          <button type="button" className="fatfish-delete-level" disabled={save.isPending || uncertain || publish.isPending || deletion.isPending} onClick={() => void deleteCurrent()}>{deleteUncertain ? t('重试同一删除', 'Retry same deletion') : t('删除关卡', 'Delete level')}</button>
        </> : null}
      </div>
      <div inert={editingLocked}><ExamplePicker onImport={(example) => {
        if (!mayReplace(t('放弃现有未保存草稿并载入示例？', 'Discard unsaved draft and load example?'))) return;
        history.commit(example.level); setTitle(example.title); setDescription(example.description); setRequiredPlacement(0);
        setServerValidation(null); setNotice(t('示例已载入草稿。发布前需重新验证并试玩。', 'Example loaded into the draft. Validate and playtest before publishing.'));
      }} /></div>
      {invalid ? <button type="button" className="fatfish-error" onClick={selectError}>{t('本地错误', 'Local error')}: {invalid}</button> : null}
      {!textValid ? <p role="alert">{t('标题最多128字节，说明最多4096字节（按UTF-8计算）。', 'Title is limited to 128 UTF-8 bytes; description to 4096 bytes.')}</p> : null}
      {serverValidation ? <p role="status">{t('服务端校验通过，内容哈希', 'Server validation passed; content hash')}: <code>{serverValidation.content_hash}</code></p> : null}
      {notice ? <p role="status">{notice}</p> : null}
      {error ? <ErrorState error={error} /> : null}
      {uncertain ? <p role="status">{t('保存结果未确定；草稿已冻结为同一请求，重试不会覆盖新内容。', 'Save outcome unknown. Retry the same request; do not overwrite with new content.')}</p> : null}
      {deleteUncertain ? <p role="status">{t('删除结果未确定，请重试同一删除。', 'Deletion outcome unknown. Retry the same deletion.')}</p> : null}
    </section>
    <div className="fatfish-editor-body">
      <div inert={editingLocked}><LevelCanvas level={visualLevel} selected={selected} grid={grid} onSelect={setSelected} onCommit={commit}
        onPlaceFish={placingFish ? placeFish : undefined} onCancelPlacement={() => setPlacingFish(false)} onDelete={removeSelected} onUndo={undo} onRedo={redo} /></div>
      <div inert={editingLocked}><ShapeInspector level={visualLevel} selected={selected} commit={commit} onDelete={removeSelected} /></div>
    </div>
    {initial?.id ? <div inert={editingLocked}><VersionShelf levelID={initial.id} /></div> : null}
  </div>;
}

export function LevelManager() {
  const t = useActivityText();
  const [page, setPage] = useState(1), [selectedID, setSelectedID] = useState<string | null>(null), [generation, setGeneration] = useState(0);
  const list = useQuery({ queryKey: ['fatfish', 'levels', page], queryFn: () => listLevels(page) });
  const detail = useQuery({ queryKey: ['fatfish', 'level', selectedID], queryFn: () => getLevel(selectedID!), enabled: !!selectedID });
  const [draftDirty, setDraftDirty] = useState(false);
  // The editor owns its browser/SPA guard; this extra confirmation covers directory changes without routing.
  const switchTo = (id: string | null) => {
    if (draftDirty && !window.confirm(t('放弃当前未保存草稿？', 'Discard unsaved draft?'))) return;
    setSelectedID(id); setGeneration((value) => value + 1); setDraftDirty(false);
  };
  return <div className="fatfish-manager">
    <aside className="fatfish-directory"><h2>{t('关卡目录', 'Level directory')}</h2>
      <button type="button" onClick={() => switchTo(null)}>{t('新建关卡', 'New level')}</button>
      {list.isPending ? <LoadingState /> : list.error ? <ErrorState error={list.error} onRetry={() => void list.refetch()} /> : <>
        <ul>{list.data?.items.map((item) => <li key={item.id}><button type="button" onClick={() => switchTo(item.id)}>{item.title} · r{item.revision}</button></li>)}</ul>
        <button type="button" disabled={page <= 1} onClick={() => setPage(page - 1)}>{t('上一页', 'Previous')}</button>
        <button type="button" disabled={!list.data?.has_more} onClick={() => setPage(page + 1)}>{t('下一页', 'Next')}</button>
      </>}
    </aside>
    {selectedID && detail.isPending ? <LoadingState /> : selectedID && detail.error ? <ErrorState error={detail.error} onRetry={() => void detail.refetch()} /> :
      <LevelDraftEditor key={`${selectedID ?? 'new'}-${generation}`} initial={selectedID ? detail.data ?? null : null}
        onDirty={setDraftDirty} onSaved={(item) => { setSelectedID(item.id); setDraftDirty(false); }}
        onDeleted={() => { setSelectedID(null); setGeneration((value) => value + 1); setDraftDirty(false); setPage(1); }} />}
  </div>;
}
