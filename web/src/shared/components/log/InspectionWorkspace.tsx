import { useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import { Button, Sheet } from '@shared/components/ui';
import './inspection.css';

interface Props {
  selected: string | null;
  title: string;
  closeLabel: string;
  onClose: () => void;
  detail: ReactNode;
  children: ReactNode;
}

/** The list and inspector share a column budget after navigation and artwork take their space. */
export function InspectionWorkspace({
  selected,
  title,
  closeLabel,
  onClose,
  detail,
  children,
}: Props) {
  const container = useRef<HTMLDivElement>(null);
  const heading = useRef<HTMLHeadingElement>(null);
  const origin = useRef<HTMLElement | null>(null);
  const previous = useRef<string | null>(null);
  const [docked, setDocked] = useState(false);
  useLayoutEffect(() => {
    const node = container.current;
    if (!node || typeof ResizeObserver === 'undefined') return;
    const measure = () => {
      const rem = parseFloat(getComputedStyle(document.documentElement).fontSize) || 16;
      setDocked(node.clientWidth >= 70 * rem);
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(node);
    return () => observer.disconnect();
  }, []);
  useLayoutEffect(() => {
    const old = previous.current;
    previous.current = selected;
    if (!selected && old) {
      const row = container.current?.querySelector<HTMLElement>(`[data-inspect-id="${old}"]`);
      const trigger = origin.current?.isConnected ? origin.current : row;
      origin.current = null;
      const frame = requestAnimationFrame(() => trigger?.focus());
      return () => cancelAnimationFrame(frame);
    }
    if (selected && docked) {
      const target = heading.current;
      if (target?.parentElement?.parentElement) target.parentElement.parentElement.scrollTop = 0;
      target?.focus({ preventScroll: true });
    }
  }, [selected, docked]);
  return (
    <div
      ref={container}
      className={
        'inspection-workspace' + (docked && selected ? ' inspection-workspace--docked' : '')
      }
    >
      <div
        className="inspection-list"
        onFocusCapture={(event) => {
          if (event.target instanceof HTMLElement && event.target.closest('[data-inspect-trigger]'))
            origin.current = event.target;
        }}
      >
        {children}
      </div>
      {selected &&
        (docked ? (
          <section
            className="inspection-dock"
            aria-label={title}
            onKeyDown={(event) => {
              if (event.key === 'Escape' && !event.defaultPrevented) {
                event.stopPropagation();
                onClose();
              }
            }}
          >
            <header className="inspection-head">
              <h2 ref={heading} tabIndex={-1}>
                {title}
              </h2>
              <Button type="button" onClick={onClose}>
                {closeLabel}
              </Button>
            </header>
            {detail}
          </section>
        ) : (
          <Sheet open title={title} size="lg" closeLabel={closeLabel} onClose={onClose}>
            {detail}
          </Sheet>
        ))}
    </div>
  );
}
