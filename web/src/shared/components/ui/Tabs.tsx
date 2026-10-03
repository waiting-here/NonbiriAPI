import { useRef, type ReactNode } from 'react';

export function Tabs<V extends string>({
  label,
  value,
  tabs,
  onChange,
}: {
  label: string;
  value: V;
  tabs: readonly { value: V; label: ReactNode; count?: number; id?: string; panelId?: string }[];
  onChange: (next: V) => void;
}) {
  const refs = useRef<(HTMLButtonElement | null)[]>([]);
  const move = (index: number) => {
    const next = (index + tabs.length) % tabs.length;
    const tab = tabs[next];
    if (!tab) return;
    refs.current[next]?.focus();
    onChange(tab.value);
  };
  return (
    <div className="nb-tabs" role="tablist" aria-label={label}>
      {tabs.map((tab, index) => (
        <button
          key={tab.value}
          ref={(node) => {
            refs.current[index] = node;
          }}
          id={tab.id}
          type="button"
          role="tab"
          aria-controls={tab.panelId}
          aria-selected={tab.value === value}
          tabIndex={tab.value === value ? 0 : -1}
          onClick={() => onChange(tab.value)}
          onKeyDown={(event) => {
            if (!['ArrowRight', 'ArrowLeft', 'Home', 'End'].includes(event.key)) return;
            event.preventDefault();
            move(
              event.key === 'Home'
                ? 0
                : event.key === 'End'
                  ? tabs.length - 1
                  : index + (event.key === 'ArrowRight' ? 1 : -1),
            );
          }}
        >
          {tab.label}
          {tab.count !== undefined ? <span className="nb-count">{tab.count}</span> : null}
        </button>
      ))}
    </div>
  );
}
