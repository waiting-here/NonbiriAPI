import { useEffect, useMemo, useState } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { ErrorState, LoadingState } from '@shared/components/States';
import { useActivityText } from '@shared/limitedactivities/copy';
import { responseOutcomeUnknown } from '@shared/operations/api';
import { useRetainedOperation } from '@shared/operations/useRetainedOperation';
import {
  getNode, getVersion, listLevels, listVersions, saveNode,
  type Amounts, type Condition, type NodeInput, type NodeRecord, type PeriodRecord,
} from './api';
import { ConditionEditor, validateCondition } from './conditions';
import { utf8Bytes } from './draft';

const zero: Amounts = { unlock_cost: '0', ticket_price: '0', first_clear_reward: '0', star_rewards: ['0', '0', '0'] };
const amountPattern = /^(0|[1-9]\d*)(?:\.[0-9]{0,2}[1-9])?$/;
export function validAmount(value: string): boolean {
  if (!amountPattern.test(value) || value.length > 42) return false;
  const [whole, frac = ''] = value.split('.');
  return BigInt(whole + frac.padEnd(3, '0')) < (1n << 127n);
}

function VersionPicker({ value, change }: { value: string; change(value: string): void }) {
  const t = useActivityText();
  const [levelPage, setLevelPage] = useState(1), [versionPage, setVersionPage] = useState(1), [selectedLevel, setSelectedLevel] = useState<string | null>(null);
  const picked = useQuery({ queryKey: ['fatfish', 'version', value], queryFn: () => getVersion(value), enabled: !!value });
  const levelID = selectedLevel ?? picked.data?.level_id ?? '';
  const levels = useQuery({ queryKey: ['fatfish', 'levels', levelPage], queryFn: () => listLevels(levelPage) });
  const versions = useQuery({ queryKey: ['fatfish', 'versions', levelID, versionPage], queryFn: () => listVersions(levelID, versionPage), enabled: !!levelID });
  return <div className="fatfish-version-picker">
    <label>{t('关卡', 'Level')}<select value={levelID} onChange={(event) => { setSelectedLevel(event.target.value); setVersionPage(1); change(''); }}>
      <option value="">{t('选择关卡', 'Choose a level')}</option>
      {levels.data?.items.map((level) => <option key={level.id} value={level.id}>{level.title}</option>)}
      {levelID && !levels.data?.items.some((level) => level.id === levelID) ? <option value={levelID}>{levelID}</option> : null}
    </select></label>
    <div className="fatfish-actions"><button type="button" disabled={levelPage <= 1} onClick={() => setLevelPage(levelPage - 1)}>{t('上一页关卡', 'Previous levels')}</button>
      <button type="button" disabled={!levels.data?.has_more} onClick={() => setLevelPage(levelPage + 1)}>{t('下一页关卡', 'Next levels')}</button></div>
    {levels.error ? <ErrorState error={levels.error} /> : null}
    {levelID ? <><label>{t('不可变版本', 'Immutable version')}<select value={value} onChange={(event) => change(event.target.value)}>
      <option value="">{t('选择版本', 'Choose a version')}</option>
      {versions.data?.items.map((version) => <option key={version.id} value={version.id}>{version.id} · {version.content_hash.slice(0, 12)}</option>)}
      {value && !versions.data?.items.some((version) => version.id === value) ? <option value={value}>{value}</option> : null}
    </select></label>
      <div className="fatfish-actions"><button type="button" disabled={versionPage <= 1} onClick={() => setVersionPage(versionPage - 1)}>{t('上一页版本', 'Previous versions')}</button>
        <button type="button" disabled={!versions.data?.has_more} onClick={() => setVersionPage(versionPage + 1)}>{t('下一页版本', 'Next versions')}</button></div>
      {versions.error ? <ErrorState error={versions.error} /> : null}
    </> : null}
    {picked.data ? <p>{t('内容哈希', 'Content hash')}: <code>{picked.data.content_hash}</code></p> : null}
  </div>;
}

export function NodeEditor({ period, nodeID, position, onSaved, onDirty }: {
  period: PeriodRecord; nodeID: string | null; position?: { id: string; x: number; y: number } | null;
  onSaved(node: NodeRecord): void; onDirty(dirty: boolean): void;
}) {
  const detail = useQuery({ queryKey: ['fatfish', 'node', period.id, nodeID], queryFn: () => getNode(period.id, nodeID!), enabled: !!nodeID });
  if (nodeID && detail.isPending) return <LoadingState />;
  if (detail.error) return <ErrorState error={detail.error} onRetry={() => void detail.refetch()} />;
  return <NodeForm key={`${period.id}-${nodeID ?? 'new'}-${detail.data?.revision ?? '0'}-${position?.id === nodeID ? `${position.x}-${position.y}` : ''}`}
    period={period} initial={detail.data ?? null} position={position} onSaved={onSaved} onDirty={onDirty} />;
}

function NodeForm({ period, initial, position, onSaved, onDirty }: {
  period: PeriodRecord; initial: NodeRecord | null; position?: { id: string; x: number; y: number } | null;
  onSaved(node: NodeRecord): void; onDirty(dirty: boolean): void;
}) {
  const t = useActivityText(), client = useQueryClient();
  const [title, setTitle] = useState(initial?.title ?? ''), [description, setDescription] = useState(initial?.description ?? '');
  const [mapX, setMapX] = useState(position && position.id === initial?.id ? position.x : initial?.map_x ?? 200);
  const [mapY, setMapY] = useState(position && position.id === initial?.id ? position.y : initial?.map_y ?? 200);
  const [order, setOrder] = useState(initial?.order ?? (period.nodes?.length ?? 0));
  const [versionID, setVersionID] = useState(initial?.version_id ?? '');
  const [condition, setCondition] = useState<Condition>(initial?.condition ?? {});
  const [hidden, setHidden] = useState(initial?.hidden ?? false);
  const [amounts, setAmounts] = useState<Amounts>(initial?.amounts ?? zero);
  const [saved, setSaved] = useState(() => JSON.stringify({ title: initial?.title ?? '', description: initial?.description ?? '', mapX: initial?.map_x ?? 200, mapY: initial?.map_y ?? 200, order: initial?.order ?? (period.nodes?.length ?? 0), versionID: initial?.version_id ?? '', condition: initial?.condition ?? {}, hidden: initial?.hidden ?? false, amounts: initial?.amounts ?? zero }));
  const [error, setError] = useState<unknown>(null);
  const snapshot = JSON.stringify({ title, description, mapX, mapY, order, versionID, condition, hidden, amounts });
  const dirty = snapshot !== saved;
  useEffect(() => onDirty(dirty), [dirty, onDirty]);
  const nodes = period.nodes ?? [];
  const validIDs = useMemo(() => new Set(nodes.map((node) => node.id)), [nodes]);
  const conditionError = validateCondition(condition, validIDs);
  const amountError = [amounts.unlock_cost, amounts.ticket_price, amounts.first_clear_reward, ...amounts.star_rewards].some((value) => !validAmount(value));
  const textValid = utf8Bytes(title) <= 128 && utf8Bytes(description) <= 4096;
  const valid = title.trim().length > 0 && textValid && !!versionID && !conditionError && !amountError && Number.isSafeInteger(mapX) && Math.abs(mapX) <= 1000000 && Number.isSafeInteger(mapY) && Math.abs(mapY) <= 1000000 && Number.isSafeInteger(order) && order >= 0 && order <= 127;
  const save = useRetainedOperation((request: { id: string | null; input: NodeInput }, key) => saveNode(period.id, request.id, request.input, key),
    () => client.invalidateQueries({ queryKey: ['fatfish', 'period', period.id] }), ['admin', 'fatfish']);
  const uncertain = save.isError && responseOutcomeUnknown(save.error);
  const editingLocked = save.isPending || uncertain;
  const submit = async () => {
    if (!valid || save.isPending) return;
    setError(null);
    const request = uncertain && save.variables ? save.variables : { id: initial?.id ?? null, input: {
      title, description, map_x: mapX, map_y: mapY, order, version_id: versionID, condition,
      hidden_until_eligible: hidden, amounts, expected_period_revision: period.revision,
      ...(initial?.revision ? { expected_revision: initial.revision } : {}),
    } };
    try {
      const result = await save.mutateAsync(request);
      client.setQueryData(['fatfish', 'node', period.id, result.id], result);
      setSaved(snapshot); onSaved(result);
    } catch (cause) { setError(cause); }
  };
  const setAmount = (name: keyof Omit<Amounts, 'star_rewards'>, value: string) => setAmounts({ ...amounts, [name]: value });
  return <section className="fatfish-node-editor"><h3>{initial ? t('编辑节点', 'Edit node') : t('新建节点', 'New node')}</h3>
    <div className="fatfish-fields" inert={editingLocked}>
      <label>{t('标题', 'Title')}<input value={title} maxLength={128} onChange={(event) => setTitle(event.target.value)} /></label>
      <label>{t('说明', 'Description')}<textarea value={description} maxLength={4096} onChange={(event) => setDescription(event.target.value)} /></label>
      <label>{t('地图 X', 'Map X')}<input type="number" min={-1000000} max={1000000} value={mapX} onChange={(event) => setMapX(Number(event.target.value))} /></label>
      <label>{t('地图 Y', 'Map Y')}<input type="number" min={-1000000} max={1000000} value={mapY} onChange={(event) => setMapY(Number(event.target.value))} /></label>
      <label>{t('排序', 'Order')}<input type="number" min={0} max={127} value={order} onChange={(event) => setOrder(Number(event.target.value))} /></label>
    </div>
    <div inert={editingLocked}><VersionPicker value={versionID} change={setVersionID} />
    <label><input type="checkbox" checked={hidden} onChange={(event) => setHidden(event.target.checked)} />{t('达到条件前隐藏', 'Hide until eligible')}</label>
    <ConditionEditor value={condition} nodes={nodes} currentID={initial?.id} onChange={setCondition} /></div>
    <fieldset disabled={editingLocked}><legend>{t('通用积分金额（最多三位小数）', 'General credit amounts (up to three decimals)')}</legend>
      <div className="fatfish-fields">
        {([['unlock_cost', t('解锁费用', 'Unlock cost')], ['ticket_price', t('每局票价', 'Ticket price')], ['first_clear_reward', t('首通奖励', 'First-clear reward')]] as const).map(([name, caption]) => <label key={name}>{caption}<input inputMode="decimal" value={amounts[name]} onChange={(event) => setAmount(name, event.target.value)} /></label>)}
        {amounts.star_rewards.map((value, index) => <label key={index}>{index + 1}★ {t('奖励', 'reward')}<input inputMode="decimal" value={value} onChange={(event) => { const next = [...amounts.star_rewards] as Amounts['star_rewards']; next[index] = event.target.value; setAmounts({ ...amounts, star_rewards: next }); }} /></label>)}
      </div>
    </fieldset>
    {conditionError ? <p role="alert">{conditionError}</p> : null}
    {!textValid ? <p role="alert">{t('标题最多128字节，说明最多4096字节（按UTF-8计算）。', 'Title is limited to 128 UTF-8 bytes; description to 4096 bytes.')}</p> : null}
    {amountError ? <p role="alert">{t('金额须为非负、规范的毫积分字符串。', 'Amounts must be canonical nonnegative milli-credit strings.')}</p> : null}
    {error ? <ErrorState error={error} /> : null}
    {uncertain ? <p role="status">{t('写入结果未确定；请用同一操作重试。', 'Write outcome unknown. Retry the same operation.')}</p> : null}
    <button type="button" disabled={!valid || save.isPending || period.state === 'closed'} onClick={() => void submit()}>{uncertain ? t('重试保存节点', 'Retry node save') : t('保存节点', 'Save node')}</button>
    {period.state === 'closed' ? <p>{t('已关闭期次不能修改节点。', 'Closed periods cannot change nodes.')}</p> : null}
  </section>;
}
