import { SimplePager } from '@shared/operations/SimplePager';
import { useEffect, useMemo, useRef, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { ErrorState, LoadingState } from '@shared/components/States';
import { toolCopy, useFatFishText } from './copy';
import { responseOutcomeUnknown } from '@shared/operations/api';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import { ENGINE_VERSION, FISH_RADIUS, type Level, type Point, type Polygon } from '@shared/fatfish/engine/types';
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
import { PlaytestPane, type PlaytestWorkspace } from './PlaytestPane';
import { PlaytestDraft } from './playtestDraft';
import { blankLevel, cloneLevel, convertToCurrentDraft, importDraft, localValidation, nextID, rect, snap, toolPolygon, utf8Bytes, type ObjectKind, type Selection, unit } from './draft';
import { useHistory } from './history';
import { useDraftGuard, useWorkspaceConfirm } from './useDraftGuard';

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
const objectLabels = { fish: 'fish', tools: 'movable_tool', solids: 'fixed_obstacle', hazards: 'hazard', bowls: 'bowl', switches: 'switch', gates: 'gate', directions: 'direction_zone' } as const;

function download(name: string, value: unknown) {
  const blob = new Blob([JSON.stringify(value, null, 2) + '\n'], { type: 'application/json' });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement('a');
  anchor.href = url; anchor.download = name; document.body.append(anchor); anchor.click(); anchor.remove();
  setTimeout(() => URL.revokeObjectURL(url), 0);
}

function VersionShelf({ levelID, onPlay }: { levelID: string; onPlay(version: VersionRecord): void }) {
  const text = useFatFishText();
  const [page, setPage] = useState(1), [versionID, setVersionID] = useState<string | null>(null);
  const versions = useQuery({ queryKey: ['fatfish', 'versions', levelID, page], queryFn: () => listVersions(levelID, page) });
  const version = useQuery({ queryKey: ['fatfish', 'version', versionID], queryFn: () => getVersion(versionID!), enabled: !!versionID });
  const proofs = useQuery({ queryKey: ['fatfish', 'playtests', versionID], queryFn: () => listPlaytests(versionID!), enabled: !!versionID });
  return <section className="fatfish-shelf">
    <h3>{text('immutable_versions_and_playtest_proofs')}</h3>
    {versions.isPending ? <LoadingState /> : versions.error ? <ErrorState error={versions.error} /> : <>
      <ul>{versions.data?.items.map((item) => <li key={item.id}>
        <button type="button" onClick={() => setVersionID(item.id)}>{text('version')} {item.version_number} · {item.playtest_summary?.best_stars ?? 0}★</button>
      </li>)}</ul>
      <SimplePager
        page={page}
        hasMore={Boolean(versions.data?.has_more)}
        onPrev={() => setPage(page - 1)}
        onNext={() => setPage(page + 1)}
        labels={{ previous: text('previous'), next: text('next') }}
      />
    </>}
    {versionID ? <div>
      {version.isPending ? <LoadingState /> : version.error ? <ErrorState error={version.error} /> : <details><summary>{text('version_details')}</summary><code>{version.data?.content_hash}</code></details>}
      {version.data ? <button type="button" onClick={() => onPlay(version.data!)}>{text('playtest_this_version')}</button> : null}
      {proofs.isPending ? <LoadingState /> : proofs.error ? <ErrorState error={proofs.error} /> : <>
        <p>{text('verified_one_star_proof_in_recent_records')}: {proofs.data?.some((item) => item.passed && item.stars >= 1) ? text('available') : text('not_shown_recently_the_server_checks_the_full_record_before_publicatio')}</p>
        <ul>{proofs.data?.map((item) => <li key={item.id}>{item.stars}★ · {formatScoreUnits(item.score_units)}</li>)}</ul>
      </>}
    </div> : null}
  </section>;
}

function LevelDraftEditor({ initial, onSaved, onDirty, onDeleted }: { initial: LevelRecord | null; onSaved(record: LevelRecord): void; onDirty(dirty: boolean): void; onDeleted(): void }) {
  const text = useFatFishText(), client = useQueryClient();
  const baseline = initial?.draft ?? blankLevel();
  const history = useHistory<Level>(baseline);
  const level = history.state.present;
  const visualLevel = useMemo(() => laidOutLevel(level), [level]);
  const [title, setTitle] = useState(initial?.title ?? ''), [description, setDescription] = useState(initial?.description ?? '');
  const [levelID, setLevelID] = useState(initial?.id ?? null);
  const [revision, setRevision] = useState(initial?.revision), [selected, setSelected] = useState<Selection | null>(null);
  const [canvasPending, setCanvasPending] = useState(false), [inspectorPending, setInspectorPending] = useState(false);
  const geometryPending = canvasPending || inspectorPending;
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
  const confirmDeletion = useWorkspaceConfirm({ title: text('delete_level'), confirmLabel: text('delete_level'), danger: true });
  const playtest = useRef<PlaytestWorkspace>(null);
  const intent = useRef<PlaytestDraft | null>(null);
  const play = useRetainedOperation(async (variables: { id: string | null; input: LevelInput; validateKey: string }, _key, context) => {
    const request = intent.current;
    if (!request || request.validateKey !== variables.validateKey) throw new Error('The draft operation has closed.');
    const version = await request.run((record, captured) => context.commit(() => {
      setLevelID(record.id); setRevision(record.revision);
      setSavedSnapshot(JSON.stringify({ title: captured.title, description: captured.description, level: captured.draft }));
      client.setQueryData(['fatfish', 'level', record.id], { ...record, draft: captured.draft });
      onSaved(record);
    }), context.assertCurrent);
    context.assertCurrent();
    await playtest.current?.prepare(version);
    context.assertCurrent();
    return version;
  }, () => undefined, ['admin', 'fatfish'], { clearSecrets: () => { intent.current = null; playtest.current?.reset(); } });
  const playThisLevel = async () => {
    if (play.isPending || invalid || !title.trim() || !textValid || requiredPlacement > level.fish.length) return;
    setError(null);
    intent.current ??= new PlaytestDraft(levelID, { title, description, draft: level, ...(revision ? { expected_revision: revision } : {}) });
    try {
      await play.mutateAsync({ id: intent.current.id, input: intent.current.input, validateKey: intent.current.validateKey });
      intent.current = null;
    } catch (cause) {
      if (!responseOutcomeUnknown(cause)) intent.current = null;
      setError(cause);
    }
  };
  const deleteUncertain = deletion.isError && responseOutcomeUnknown(deletion.error);
  const uncertain = save.isError && responseOutcomeUnknown(save.error);
  const editingLocked = save.isPending || uncertain || deletion.isPending || deleteUncertain;
  const { mayReplace, dialog: leaveDialog } = useDraftGuard(dirty || editingLocked || play.isPending || geometryPending);
  useEffect(() => onDirty(dirty || editingLocked || play.isPending || geometryPending), [dirty, editingLocked, play.isPending, geometryPending, onDirty]);
  const commit = (next: Level) => { if (editingLocked) return; history.commit(next); setServerValidation(null); setNotice(''); setError(null); };
  const [geometryGeneration, setGeometryGeneration] = useState(0);
  const clearGeometry = () => { setGeometryGeneration((value) => value + 1); setCanvasPending(false); setInspectorPending(false); };
  const undo = () => { if (!editingLocked) { clearGeometry(); history.undo(); setServerValidation(null); setNotice(''); setError(null); } };
  const redo = () => { if (!editingLocked) { clearGeometry(); history.redo(); setServerValidation(null); setNotice(''); setError(null); } };
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
    const request = uncertain && save.variables ? save.variables : { id: levelID, input: { title, description, draft: level, ...(revision ? { expected_revision: revision } : {}) } };
    try {
      const result = await save.mutateAsync(request);
      const clean = JSON.stringify({ title: request.input.title, description: request.input.description, level: request.input.draft });
      setSavedSnapshot(clean); setRevision(result.revision); setLevelID(result.id); setNotice(text('draft_saved'));
      client.setQueryData(['fatfish', 'level', result.id], { ...result, draft: request.input.draft });
      onSaved(result);
    } catch (cause) { setError(cause); }
  };
  const validation = useRetainedOperation((draft: Level, key) => validateLevelOnServer(draft, key),
    () => undefined, ['admin', 'fatfish']);
  const validate = async () => {
    if (invalid) return;
    setError(null);
    try { setServerValidation(await validation.mutateAsync(level)); }
    catch (cause) { setError(cause); }
  };
  const publishCurrent = async () => {
    if (!initial?.id || !revision || dirty || publish.isPending || editingLocked) return;
    setError(null);
    try { const value = await publish.mutateAsync({ id: initial.id, revision }); setNotice(`${text('version_published')}: ${value.version_number}`); }
    catch (cause) { setError(cause); }
  };
  const deleteCurrent = async () => {
    if (!initial?.id || !revision || deletion.isPending || save.isPending || uncertain || publish.isPending) return;
    if (!deleteUncertain && !await confirmDeletion.confirm(text('delete_level_body'))) return;
    setError(null);
    try {
      await deletion.mutateAsync(deleteUncertain && deletion.variables ? deletion.variables : { id: initial.id, revision });
      client.removeQueries({ queryKey: ['fatfish', 'level', initial.id], exact: true });
      onDirty(false); onDeleted();
    } catch (cause) { setError(cause); }
  };
  const importFile = async (file: File | undefined) => {
    if (!file) return;
    if (!await mayReplace(text('discard_unsaved_draft_and_import'))) return;
    setError(null);
    try {
      if (file.size > 256 * 1024) throw new Error('Import exceeds 256 KiB');
      const converted = importDraft(await file.text());
      history.commit(converted.level); setTitle(converted.title); setDescription(converted.description);
      setRequiredPlacement(converted.pendingFishCount); setNotice(converted.warning ?? text('imported_revalidate_before_publishing'));
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
  return <div className="fatfish-editor">{leaveDialog}{confirmDeletion.dialog}
    <section className="fatfish-toolbar">
      <h2>{initial ? text('edit_level_draft') : text('new_level_draft')}</h2>
      <p>{text('draft_rules_version')}: {level.engine_version} · {level.engine_version !== ENGINE_VERSION ? text('legacy') : text('current')}</p>
      {level.engine_version !== ENGINE_VERSION ? <div>
        <button type="button" disabled={editingLocked} onClick={() => commit(convertToCurrentDraft(level))}>{text('convert_to_new_draft')}</button>
        <p>{text('conversion_keeps_the_layout_speed_and_other_settings')}</p>
      </div> : null}
      <p>{text('after_editing_save_and_publish_a_new_version_then_playtest_it_existing')}</p>
      <div className="fatfish-fields" inert={editingLocked}>
        <label>{text('title')}<input value={title} maxLength={128} onChange={(event) => { setTitle(event.target.value); setNotice(''); }} /></label>
        <label>{text('description')}<textarea value={description} maxLength={4096} onChange={(event) => { setDescription(event.target.value); setNotice(''); }} /></label>
      </div>
      <details className="fatfish-level-settings" inert={editingLocked}><summary>{text('duration_speed_and_star_thresholds')}</summary><div className="fatfish-fields">
        <label>{text('duration_seconds')}<input type="number" min={10} max={600} value={level.duration_seconds} onChange={(event) => { const next = cloneLevel(level); next.duration_seconds = Number(event.target.value); commit(next); }} /></label>
        <label>{text('speed_pixels_second')}<input type="number" min={16} max={160} value={level.speed_pixels_per_second} onChange={(event) => { const next = cloneLevel(level); next.speed_pixels_per_second = Number(event.target.value); commit(next); }} /></label>
        {level.thresholds.map((value, index) => <label key={index}>{index + 1}★ {text('fish_required')}<input type="number" min={1} max={level.fish.length} value={value} onChange={(event) => { const next = cloneLevel(level); next.thresholds[index] = Number(event.target.value); commit(next); }} /></label>)}
      </div></details>
      <div className="fatfish-actions" inert={editingLocked}><label><input type="checkbox" checked={grid} onChange={(event) => setGrid(event.target.checked)} />{text('snap_to_8_pixel_grid')}</label>
        <button type="button" disabled={!history.state.past.length} onClick={undo}>{text('undo')}</button>
        <button type="button" disabled={!history.state.future.length} onClick={redo}>{text('redo')}</button>
        <label>{text('new_fish_heading')}<input type="number" min={0} max={4095} value={fishHeading} onChange={(event) => setFishHeading(Number(event.target.value))} /></label>
      </div>
      <div className="fatfish-actions" inert={editingLocked}>{objectKinds.map((kind) => <button key={kind} type="button" disabled={level[kind].length >= limits[kind]}
        onClick={() => {
          if (kind === 'fish') { setPlacingFish(true); return; }
          const next = addObject(level, kind); commit(next.level); setSelected(next.selection);
        }}>{text('add')} {text(objectLabels[kind])}</button>)}</div>
      <div className="fatfish-tool-palette" inert={editingLocked} aria-label={text('add_movable_pieces')}>
        {Object.keys(toolNames).map((key) => <button key={key} type="button" disabled={level.tools.length >= limits.tools} onClick={() => {
          const next = addObject(level, 'tools', key); commit(next.level); setSelected(next.selection);
        }}><img src={`/assets/fatfish/svg/${toolIcon(key)}.svg`} alt="" />{text(toolCopy[key])}</button>)}
      </div>
      {placingFish ? <p role="status">{text('click_the_map_to_place_the_next_fish')}</p> : null}
      {requiredPlacement > 0 ? <p role="status">{text('legacy_fish_must_be_placed_individually')}: {level.fish.length}/{requiredPlacement}. {text('old_passed_results_do_not_count_as_proof')}</p> : null}
      <div className="fatfish-actions">
        <button type="button" disabled={play.isPending || editingLocked || geometryPending || !!invalid || !title.trim() || !textValid} onClick={() => void playThisLevel()}>{play.isError && responseOutcomeUnknown(play.error) ? text('continue_preparing_this_playtest') : text('playtest_this_level')}</button>
        <label className="fatfish-file" inert={editingLocked}>{text('import_json_convert_v0_6')}<input type="file" accept=".json,application/json" disabled={editingLocked} onChange={(event) => { void importFile(event.target.files?.[0]); event.target.value = ''; }} /></label>
        <button type="button" disabled={editingLocked || geometryPending || !!invalid} onClick={() => download(`${title.trim() || 'fatfish-level'}.json`, { title, description, draft: level })}>{text('export_draft')}</button>
        <button type="button" disabled={editingLocked || validation.isPending || geometryPending || !!invalid} onClick={() => void validate()}>{text('validate_on_server')}</button>
        <button type="button" disabled={geometryPending || !!invalid || !title.trim() || !textValid || requiredPlacement > level.fish.length || save.isPending || deletion.isPending || deleteUncertain || (uncertain && !save.variables)} onClick={() => void submit()}>{uncertain ? text('retry_same_save') : text('save_draft')}</button>
        {initial?.id ? <>
          <button type="button" disabled={geometryPending || dirty || !!invalid || publish.isPending || editingLocked} onClick={() => void publishCurrent()}>{text('publish_immutable_version_from_saved_draft')}</button>
          <button type="button" className="fatfish-delete-level" disabled={save.isPending || uncertain || publish.isPending || deletion.isPending} onClick={() => void deleteCurrent()}>{deleteUncertain ? text('retry_same_deletion') : text('delete_level')}</button>
        </> : null}
      </div>
      <div inert={editingLocked}><ExamplePicker onImport={async (example) => {
        if (!await mayReplace(text('discard_unsaved_draft_and_load_example'))) return;
        history.commit(example.level); setTitle(example.title); setDescription(example.description); setRequiredPlacement(0);
        setServerValidation(null); setNotice(text('example_loaded_into_the_draft_validate_and_playtest_before_publishing'));
      }} /></div>
      {invalid ? <button type="button" className="fatfish-error" onClick={selectError}>{text('local_error')}: {invalid}</button> : null}
      {!textValid ? <p role="alert">{text('title_is_limited_to_128_utf_8_bytes_description_to_4096_bytes')}</p> : null}
      {serverValidation ? <details><summary>{text('server_validation_passed_content_hash')}</summary><code>{serverValidation.content_hash}</code></details> : null}
      {notice ? <p role="status">{notice}</p> : null}
      {error ? <ErrorState error={error} /> : null}
      {uncertain ? <p role="status">{text('save_outcome_unknown_retry_the_same_request_do_not_overwrite_with_new_')}</p> : null}
      {deleteUncertain ? <p role="status">{text('deletion_outcome_unknown_retry_the_same_deletion')}</p> : null}
    </section>
    <div className="fatfish-editor-body">
      <div inert={editingLocked}><LevelCanvas key={geometryGeneration} level={visualLevel} selected={selected} grid={grid} onSelect={setSelected} onCommit={commit} onPendingChange={setCanvasPending}
        onPlaceFish={placingFish ? placeFish : undefined} onCancelPlacement={() => setPlacingFish(false)} onDelete={removeSelected} onUndo={undo} onRedo={redo} /></div>
      <div inert={editingLocked}><ShapeInspector key={geometryGeneration + ':' + (selected?.kind ?? '') + ':' + (selected?.id ?? '')} level={visualLevel} selected={selected} commit={commit} onDelete={removeSelected} onPendingChange={setInspectorPending} /></div>
    </div>
    <PlaytestPane ref={playtest} />
    {levelID ? <div inert={editingLocked}><VersionShelf levelID={levelID} onPlay={(version) => { void playtest.current?.prepare(version).catch(setError); }} /></div> : null}
  </div>;
}

export function LevelManager() {
  const text = useFatFishText();
  const [page, setPage] = useState(1), [selectedID, setSelectedID] = useState<string | null>(null), [generation, setGeneration] = useState(0);
  const list = useQuery({ queryKey: ['fatfish', 'levels', page], queryFn: () => listLevels(page) });
  const detail = useQuery({ queryKey: ['fatfish', 'level', selectedID], queryFn: () => getLevel(selectedID!), enabled: !!selectedID });
  const [draftDirty, setDraftDirty] = useState(false);
  // The editor owns its browser/SPA guard; this extra confirmation covers directory changes without routing.
  const switching = useWorkspaceConfirm();
  const switchTo = async (id: string | null) => {
    if (draftDirty && !await switching.confirm(text('discard_unsaved_draft'))) return;
    setSelectedID(id); setGeneration((value) => value + 1); setDraftDirty(false);
  };
  return <div className="fatfish-manager">{switching.dialog}
    <aside className="fatfish-directory"><h2>{text('level_directory')}</h2>
      <button type="button" onClick={() => switchTo(null)}>{text('new_level')}</button>
      {list.isPending ? <LoadingState /> : list.error ? <ErrorState error={list.error} onRetry={() => void list.refetch()} /> : <>
        <ul>{list.data?.items.map((item) => <li key={item.id}><button type="button" onClick={() => switchTo(item.id)}>{item.title} · r{item.revision}</button></li>)}</ul>
        <SimplePager
          page={page}
          hasMore={Boolean(list.data?.has_more)}
          onPrev={() => setPage(page - 1)}
          onNext={() => setPage(page + 1)}
          labels={{ previous: text('previous'), next: text('next') }}
        />
      </>}
    </aside>
    {selectedID && detail.isPending ? <LoadingState /> : selectedID && detail.error ? <ErrorState error={detail.error} onRetry={() => void detail.refetch()} /> :
      <LevelDraftEditor key={generation} initial={selectedID ? detail.data ?? null : null}
        onDirty={setDraftDirty} onSaved={(item) => { setSelectedID(item.id); }}
        onDeleted={() => { setSelectedID(null); setGeneration((value) => value + 1); setDraftDirty(false); setPage(1); }} />}
  </div>;
}
