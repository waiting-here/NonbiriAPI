import { useRef, useState, type PointerEvent } from 'react';
import { useActivityText } from '@shared/limitedactivities/copy';
import type { Condition, NodeRecord } from './api';
import { conditionReferences } from './conditions';

interface Props {
  nodes: NodeRecord[]; selectedID: string | null; selectedCondition?: Condition;
  onSelect(id: string): boolean; onMove(id: string, x: number, y: number): void;
}
export function NodeMap({ nodes, selectedID, selectedCondition, onSelect, onMove }: Props) {
  const t = useActivityText();
  const [zoom, setZoom] = useState(1), [origin, setOrigin] = useState({ x: 0, y: 0 });
  const [preview, setPreview] = useState<{ id: string; x: number; y: number } | null>(null);
  const drag = useRef<{ id: string; x: number; y: number; pointerX: number; pointerY: number } | null>(null);
  const stage = useRef<SVGSVGElement>(null);
  const at = (event: PointerEvent<SVGElement>) => {
    const bounds = stage.current?.getBoundingClientRect();
    if (!bounds) return { x: 0, y: 0 };
    return { x: origin.x + (event.clientX - bounds.left) * (800 / zoom) / bounds.width,
      y: origin.y + (event.clientY - bounds.top) * (500 / zoom) / bounds.height };
  };
  const onPointerMove = (event: PointerEvent<SVGSVGElement>) => {
    if (!drag.current) return;
    const point = at(event), active = drag.current;
    setPreview({ id: active.id, x: Math.round((active.x + point.x - active.pointerX) / 8) * 8,
      y: Math.round((active.y + point.y - active.pointerY) / 8) * 8 });
  };
  const finish = () => {
    if (preview) onMove(preview.id, preview.x, preview.y);
    drag.current = null; setPreview(null);
  };
  const position = (node: NodeRecord) => preview?.id === node.id ? { x: preview.x, y: preview.y } : { x: node.map_x, y: node.map_y };
  const selected = nodes.find((node) => node.id === selectedID);
  const references = selectedCondition ? conditionReferences(selectedCondition) : [];
  return <section className="fatfish-node-map">
    <div className="fatfish-actions"><strong>{t('期次图', 'Period map')}</strong>
      <button type="button" onClick={() => setZoom(Math.max(0.5, zoom / 1.25))}>−</button>
      <span>{Math.round(zoom * 100)}%</span>
      <button type="button" onClick={() => setZoom(Math.min(3, zoom * 1.25))}>+</button>
      <button type="button" onClick={() => setOrigin({ x: origin.x - 100 / zoom, y: origin.y })}>←</button>
      <button type="button" onClick={() => setOrigin({ x: origin.x + 100 / zoom, y: origin.y })}>→</button>
      <button type="button" onClick={() => setOrigin({ x: origin.x, y: origin.y - 100 / zoom })}>↑</button>
      <button type="button" onClick={() => setOrigin({ x: origin.x, y: origin.y + 100 / zoom })}>↓</button>
    </div>
    <svg ref={stage} viewBox={`${origin.x} ${origin.y} ${800 / zoom} ${500 / zoom}`}
      role="img" aria-label={t('可拖动节点布局', 'Draggable node layout')}
      onPointerMove={onPointerMove} onPointerUp={finish}
      onPointerCancel={() => { drag.current = null; setPreview(null); }}>
      <rect x={origin.x} y={origin.y} width={800 / zoom} height={500 / zoom} fill="#f7fafb" />
      {selected ? references.map((id) => {
        const source = nodes.find((node) => node.id === id);
        return source ? <line key={id} x1={position(source).x} y1={position(source).y}
          x2={position(selected).x} y2={position(selected).y}
          stroke="#55848a" strokeWidth={2 / zoom} /> : null;
      }) : null}
      {nodes.map((node) => {
        const positionValue = preview?.id === node.id ? preview : { x: node.map_x, y: node.map_y };
        return <g key={node.id} transform={`translate(${positionValue.x} ${positionValue.y})`}
          onPointerDown={(event) => {
            event.stopPropagation();
            if (!onSelect(node.id)) return;
            const pointer = at(event);
            drag.current = { id: node.id, x: node.map_x, y: node.map_y, pointerX: pointer.x, pointerY: pointer.y };
            stage.current?.setPointerCapture(event.pointerId);
          }}>
          <circle r={24 / zoom} fill={node.id === selectedID ? '#145f85' : '#439b8f'} stroke="white" strokeWidth={3 / zoom} />
          <text textAnchor="middle" dy={4 / zoom} fontSize={12 / zoom} fill="white" pointerEvents="none">{node.order + 1}</text>
        </g>;
      })}
    </svg>
    <ul className="fatfish-node-list">{nodes.map((node) => <li key={node.id}>
      <button type="button" aria-current={selectedID === node.id ? 'true' : undefined} onClick={() => onSelect(node.id)}>
        {node.order + 1}. {node.title} · ({node.map_x}, {node.map_y})
      </button>
    </li>)}</ul>
  </section>;
}
