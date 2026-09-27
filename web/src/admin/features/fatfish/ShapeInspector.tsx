import type { Level, Point, Shape, Switch, Gate, Direction } from '@shared/fatfish/engine/types';
import { useActivityText } from '@shared/limitedactivities/copy';
import { cloneLevel, px, rect, type Selection, unit } from './draft';

type Editable = Shape & {
  resource_key?: string; placed?: boolean; x?: number; y?: number;
  required?: number; capacity?: number; mode?: Switch['mode'] | Gate['mode'] | Direction['mode'];
  initially_open?: boolean; switch_ids?: number[]; heading?: number;
};
function itemFor(level: Level, selected: Selection): Editable | null {
  if (selected.kind === 'fish') return null;
  return level[selected.kind].find((item) => item.id === selected.id) as Editable | undefined ?? null;
}
function NumberField({ label, value, min, max, onChange }: {
  label: string; value: number; min?: number; max?: number; onChange(value: number): void;
}) {
  return <label>{label}<input type="number" value={value} min={min} max={max} step="1"
    onChange={(event) => { const next = Number(event.target.value); if (Number.isSafeInteger(next)) onChange(next); }} /></label>;
}
function PointFields({ label, value, change }: { label: string; value: Point; change(value: Point): void }) {
  return <div className="fatfish-coordinate"><span>{label}</span>
    <NumberField label="X" value={px(value.x)} onChange={(x) => change({ ...value, x: unit(x) })} />
    <NumberField label="Y" value={px(value.y)} onChange={(y) => change({ ...value, y: unit(y) })} /></div>;
}

export function ShapeInspector({ level, selected, commit, onDelete }: {
  level: Level; selected: Selection | null; commit(next: Level): void; onDelete(): void;
}) {
  const t = useActivityText();
  if (!selected) return <p>{t('在画布上选择对象，或从上方新增对象。', 'Select an object on the map or add one above.')}</p>;
  const mutateFish = (change: (fish: Level['fish'][number]) => void) => {
    const next = cloneLevel(level), fish = next.fish.find((item) => item.id === selected.id);
    if (fish) { change(fish); commit(next); }
  };
  const mutate = (change: (item: Editable) => void) => {
    if (selected.kind === 'fish') return;
    const next = cloneLevel(level), item = itemFor(next, selected);
    if (item) { change(item); commit(next); }
  };
  const fish = selected.kind === 'fish' ? level.fish.find((item) => item.id === selected.id) : undefined;
  const item = selected.kind === 'fish' ? null : itemFor(level, selected);
  const editVertex = (hole: number | null, index: number, value: Point) => mutate((shape) => {
    const ring = hole === null ? shape.polygon.outer : shape.polygon.holes[hole];
    ring[index] = value;
  });
  const addVertex = (hole: number | null) => mutate((shape) => {
    const ring = hole === null ? shape.polygon.outer : shape.polygon.holes[hole];
    if (ring.length >= (hole === null ? 128 : 64)) return;
    const a = ring[ring.length - 1], b = ring[0];
    ring.push({ x: Math.round((a.x + b.x) / 2), y: Math.round((a.y + b.y) / 2) });
  });
  const removeVertex = (hole: number | null, index: number) => mutate((shape) => {
    const ring = hole === null ? shape.polygon.outer : shape.polygon.holes[hole];
    if (ring.length > 3) ring.splice(index, 1);
  });
  const renderRing = (ring: Point[], hole: number | null) => <fieldset key={hole ?? 'outer'}>
    <legend>{hole === null ? t('外轮廓', 'Outer contour') : `${t('洞', 'Hole')} ${hole + 1}`}</legend>
    {ring.map((vertex, index) => <div className="fatfish-vertex" key={index}>
      <PointFields label={`${index + 1}`} value={vertex} change={(point) => editVertex(hole, index, point)} />
      <button type="button" disabled={ring.length <= 3} onClick={() => removeVertex(hole, index)} aria-label={`${t('删除顶点', 'Remove vertex')} ${index + 1}`}>×</button>
    </div>)}
    <button type="button" disabled={ring.length >= (hole === null ? 128 : 64)} onClick={() => addVertex(hole)}>{t('新增顶点', 'Add vertex')}</button>
    {hole !== null ? <button type="button" onClick={() => mutate((shape) => { shape.polygon.holes.splice(hole, 1); })}>{t('删除洞', 'Remove hole')}</button> : null}
  </fieldset>;
  return <section className="fatfish-inspector" aria-label={t('对象属性', 'Object properties')}>
    <h3>{selected.kind} #{selected.id}</h3>
    {fish ? <>
      <PointFields label={t('起始位置', 'Starting position')} value={fish} change={(point) => mutateFish((entry) => { entry.x = point.x; entry.y = point.y; })} />
      <NumberField label={t('朝向（0–4095，顺时针）', 'Heading (0–4095, clockwise)')} value={fish.heading} min={0} max={4095} onChange={(value) => mutateFish((entry) => { entry.heading = value; })} />
    </> : null}
    {item && selected.kind === 'tools' ? <>
      <label>{t('工具类型', 'Tool type')}<select value={item.resource_key} onChange={(event) => mutate((shape) => { shape.resource_key = event.target.value; })}>
        {['barrier', 'memory', 'fan', 'light', 'cup'].map((key) => <option key={key} value={key}>{key}</option>)}
      </select></label>
      <label><input type="checkbox" checked={!!item.placed} onChange={(event) => mutate((shape) => { shape.placed = event.target.checked; })} />{t('已放在场内（可移动）', 'Placed in field (movable)')}</label>
      <PointFields label={t('放置中心', 'Placed center')} value={{ x: item.x ?? 0, y: item.y ?? 0 }} change={(point) => mutate((shape) => { shape.x = point.x; shape.y = point.y; })} />
    </> : null}
    {item && selected.kind === 'bowls' ? <>
      <NumberField label={t('最低收集量', 'Required fish')} value={item.required ?? 0} min={0} max={40} onChange={(value) => mutate((shape) => { shape.required = value; })} />
      <NumberField label={t('容量', 'Capacity')} value={item.capacity ?? 1} min={1} max={40} onChange={(value) => mutate((shape) => { shape.capacity = value; })} />
    </> : null}
    {item && selected.kind === 'switches' ? <label>{t('开关模式', 'Switch mode')}<select value={item.mode} onChange={(event) => mutate((shape) => { shape.mode = event.target.value as Switch['mode']; })}>
      <option value="latch">{t('触碰锁定', 'Latch')}</option><option value="hold">{t('保持触发', 'Hold')}</option>
    </select></label> : null}
    {item && selected.kind === 'gates' ? <>
      <label><input type="checkbox" checked={!!item.initially_open} onChange={(event) => mutate((shape) => { shape.initially_open = event.target.checked; })} />{t('初始开启', 'Initially open')}</label>
      <label>{t('控制规则', 'Control rule')}<select value={item.mode} onChange={(event) => mutate((shape) => { shape.mode = event.target.value as Gate['mode']; })}>
        <option value="any">{t('任一开关', 'Any switch')}</option><option value="all">{t('所有开关', 'All switches')}</option>
      </select></label>
      <fieldset><legend>{t('关联开关', 'Linked switches')}</legend>{level.switches.map((entry) => <label key={entry.id}>
        <input type="checkbox" checked={item.switch_ids?.includes(entry.id) ?? false} onChange={(event) => mutate((shape) => {
          const ids = shape.switch_ids ?? [];
          shape.switch_ids = event.target.checked ? [...ids, entry.id] : ids.filter((id) => id !== entry.id);
        })} />#{entry.id}</label>)}</fieldset>
    </> : null}
    {item && selected.kind === 'directions' ? <>
      <label>{t('方向模式', 'Direction mode')}<select value={item.mode} onChange={(event) => mutate((shape) => { shape.mode = event.target.value as Direction['mode']; })}>
        <option value="entry">{t('进入时转向', 'Redirect on entry')}</option><option value="oneway">{t('单向通行', 'One-way')}</option>
      </select></label>
      <NumberField label={t('朝向（0–4095）', 'Heading (0–4095)')} value={item.heading ?? 0} min={0} max={4095} onChange={(value) => mutate((shape) => { shape.heading = value; })} />
    </> : null}
    {item ? <>
      <p>{selected.kind === 'tools' ? t('轮廓坐标相对于工具中心。', 'Contour coordinates are relative to the tool center.') : t('轮廓坐标是场地坐标。', 'Contour coordinates use field coordinates.')}</p>
      {renderRing(item.polygon.outer, null)}
      {item.polygon.holes.map((hole, index) => renderRing(hole, index))}
      <button type="button" disabled={item.polygon.holes.length >= 8} onClick={() => mutate((shape) => {
        const xs = shape.polygon.outer.map((point) => point.x), ys = shape.polygon.outer.map((point) => point.y);
        const x = (Math.min(...xs) + Math.max(...xs)) / 2, y = (Math.min(...ys) + Math.max(...ys)) / 2;
        shape.polygon.holes.push(rect(Math.round(x), Math.round(y), unit(12), unit(12)).outer);
      })}>{t('新增洞', 'Add hole')}</button>
    </> : null}
    <button type="button" onClick={onDelete}>{t('删除对象', 'Delete object')}</button>
  </section>;
}
