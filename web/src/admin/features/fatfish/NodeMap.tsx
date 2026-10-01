import { useRef, useState, type PointerEvent } from 'react';
import { useFatFishText } from './copy';
import type { Condition, NodeRecord } from './api';
import { conditionBadges, conditionEdges, type ConditionPath } from './conditionEdges';

interface Props {
  nodes: NodeRecord[]; selectedID: string | null; selectedCondition?: Condition;
  onConnect(source: string, target: string): void; onRemove(target: string, path: ConditionPath): void;
  onSelect(id: string): boolean | Promise<boolean>; onMove(id: string, x: number, y: number): void;
}
export function NodeMap({ nodes, selectedID, selectedCondition, onSelect, onMove, onConnect, onRemove }: Props) {
  const text = useFatFishText();
  const [zoom, setZoom] = useState(1), [origin, setOrigin] = useState({ x: 0, y: 0 });
  const [preview, setPreview] = useState<{ id: string; x: number; y: number } | null>(null);
  const connection = useRef<string | null>(null);
  const selectingPointer = useRef<number | null>(null);
  const [connectionPoint, setConnectionPoint] = useState<{ source: string; x: number; y: number } | null>(null);
  const [linkSource, setLinkSource] = useState<string | null>(null);
  const drag = useRef<{ id: string; x: number; y: number; pointerX: number; pointerY: number } | null>(null);
  const stage = useRef<SVGSVGElement>(null);
  const at = (event: PointerEvent<SVGElement>) => {
    const bounds = stage.current?.getBoundingClientRect();
    if (!bounds) return { x: 0, y: 0 };
    return { x: origin.x + (event.clientX - bounds.left) * (800 / zoom) / bounds.width,
      y: origin.y + (event.clientY - bounds.top) * (500 / zoom) / bounds.height };
  };
  const onPointerMove = (event: PointerEvent<SVGSVGElement>) => {
    if (connection.current) { setConnectionPoint({ ...at(event), source: connection.current }); return; }
    if (!drag.current) return;
    const point = at(event), active = drag.current;
    setPreview({ id: active.id, x: Math.round((active.x + point.x - active.pointerX) / 8) * 8,
      y: Math.round((active.y + point.y - active.pointerY) / 8) * 8 });
  };
  const finish = (event: PointerEvent<SVGSVGElement>) => {
    selectingPointer.current = null;
    if (connection.current) {
      const point = at(event), source = connection.current;
      const target = nodes.find((node) => Math.hypot(node.map_x - point.x, node.map_y - point.y) <= 30 / zoom);
      connection.current = null; setConnectionPoint(null);
      if (target) onConnect(source, target.id);
      return;
    }
    if (preview) onMove(preview.id, preview.x, preview.y);
    drag.current = null; setPreview(null);
  };
  const position = (node: NodeRecord) => preview?.id === node.id ? { x: preview.x, y: preview.y } : { x: node.map_x, y: node.map_y };
  const references = nodes.flatMap((target) => conditionEdges(target.id === selectedID && selectedCondition ? selectedCondition : target.condition ?? {}).map((edge) => ({ ...edge, target })));
  return <section className="fatfish-node-map">
    <div className="fatfish-actions"><strong>{text('period_map')}</strong>
      <button type="button" onClick={() => setZoom(Math.max(0.5, zoom / 1.25))}>−</button>
      <span>{Math.round(zoom * 100)}%</span>
      <button type="button" onClick={() => setZoom(Math.min(3, zoom * 1.25))}>+</button>
      <button type="button" onClick={() => setOrigin({ x: origin.x - 100 / zoom, y: origin.y })}>←</button>
      <button type="button" onClick={() => setOrigin({ x: origin.x + 100 / zoom, y: origin.y })}>→</button>
      <button type="button" onClick={() => setOrigin({ x: origin.x, y: origin.y - 100 / zoom })}>↑</button>
      <button type="button" onClick={() => setOrigin({ x: origin.x, y: origin.y + 100 / zoom })}>↓</button>
    </div>
    <svg ref={stage} viewBox={`${origin.x} ${origin.y} ${800 / zoom} ${500 / zoom}`}
      role="img" aria-label={text('draggable_node_layout')}
      onPointerMove={onPointerMove} onPointerUp={finish}
      onPointerCancel={() => { selectingPointer.current = null; drag.current = null; connection.current = null; setConnectionPoint(null); setPreview(null); }}>
      <defs><marker id="fatfish-graph-arrow" markerWidth="8" markerHeight="8" refX="7" refY="4" orient="auto"><path d="M0,0 L8,4 L0,8" fill="#55848a" /></marker></defs>
      <rect x={origin.x} y={origin.y} width={800 / zoom} height={500 / zoom} fill="#f7fafb" />
      {references.map((edge) => {
        const source = nodes.find((node) => node.id === edge.source);
        if (!source) return null;
        const from = position(source), to = position(edge.target);
        const key = edge.target.id + JSON.stringify(edge.path);
        if (source.id === edge.target.id) return <path key={key} d={`M ${from.x - 16 / zoom} ${from.y - 18 / zoom} C ${from.x - 80 / zoom} ${from.y - 90 / zoom}, ${from.x + 80 / zoom} ${from.y - 90 / zoom}, ${from.x + 16 / zoom} ${from.y - 18 / zoom}`} fill="none" stroke="#55848a" markerEnd="url(#fatfish-graph-arrow)" />;
        const length = Math.hypot(to.x - from.x, to.y - from.y);
        if (length === 0) return null;
        const dx = (to.x - from.x) / length, dy = (to.y - from.y) / length;
        return <line key={key} x1={from.x + dx * 25 / zoom} y1={from.y + dy * 25 / zoom}
          x2={to.x - dx * 28 / zoom} y2={to.y - dy * 28 / zoom}
          stroke="#55848a" strokeWidth={2 / zoom} markerEnd="url(#fatfish-graph-arrow)" />;
      })}
      {connectionPoint ? (() => {
        const source = nodes.find((node) => node.id === connectionPoint.source)!;
        return <line x1={source.map_x} y1={source.map_y} x2={connectionPoint.x} y2={connectionPoint.y} stroke="#db5d34" strokeDasharray="6 4" />;
      })() : null}
      {nodes.map((node) => {
        const positionValue = preview?.id === node.id ? preview : { x: node.map_x, y: node.map_y };
        return <g key={node.id} transform={`translate(${positionValue.x} ${positionValue.y})`}
          onPointerDown={async (event) => {
            event.stopPropagation();
            if (linkSource) { onConnect(linkSource, node.id); setLinkSource(null); return; }
            selectingPointer.current = event.pointerId;
            const pointerID = event.pointerId;
            if (!await onSelect(node.id) || selectingPointer.current !== pointerID) return;
            const pointer = at(event);
            drag.current = { id: node.id, x: node.map_x, y: node.map_y, pointerX: pointer.x, pointerY: pointer.y };
            stage.current?.setPointerCapture(event.pointerId);
          }}>
          <circle r={24 / zoom} fill={node.id === selectedID ? '#145f85' : '#439b8f'} stroke="white" strokeWidth={3 / zoom} />
          <circle cx={32 / zoom} r={7 / zoom} fill="#fff" stroke="#145f85" aria-label={text('connect_from_here') + ' ' + node.title}
            onPointerDown={(event) => {
              event.stopPropagation(); connection.current = node.id; setConnectionPoint({ source: node.id, x: node.map_x, y: node.map_y });
              stage.current?.setPointerCapture(event.pointerId);
            }} />
          <text textAnchor="middle" dy={4 / zoom} fontSize={12 / zoom} fill="white" pointerEvents="none">{node.order + 1}</text>
        </g>;
      })}
    </svg>
    <p>{text('connecting_one_node_to_another_makes_passing_the_source_a_prerequisite')}</p>
    <ul className="fatfish-node-list">{nodes.map((node) => <li key={node.id}>
      <button type="button" aria-current={selectedID === node.id ? 'true' : undefined} onClick={() => onSelect(node.id)}>
        {node.order + 1}. {node.title} · ({node.map_x}, {node.map_y})
      </button>
      <button type="button" onClick={() => setLinkSource(node.id)}>{text('connect_from_here')}</button>
      <span>{conditionBadges(node.id === selectedID && selectedCondition ? selectedCondition : node.condition ?? {}).map((badge) => text(badge.kind) + (badge.min !== undefined ? ' ≥ ' + badge.min : '')).join(' · ')}</span>
    </li>)}</ul>
    {linkSource ? <button type="button" onClick={() => setLinkSource(null)}>{text('cancel_connection')}</button> : null}
    <ul>{references.map((edge) => <li key={edge.target.id + JSON.stringify(edge.path)}>
      {nodes.find((node) => node.id === edge.source)?.title} → {edge.target.title} · {text(edge.kind === 'passed' ? 'passed_node' : 'node_stars')}{edge.min ? ' ≥ ' + edge.min : ''}
      <small>{edge.path.map((part) => text(part.group === 'all' ? 'all_of' : 'any_of') + ' · ' + (part.index + 1)).join(' → ')}</small>
      <button type="button" onClick={() => onRemove(edge.target.id, edge.path)}>{text('remove_this_condition_edge')}</button>
    </li>)}</ul>
  </section>;
}
