import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react';
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
  const list = useRef<HTMLDivElement>(null);
  const heading = useRef<HTMLHeadingElement>(null);
  const origin = useRef<HTMLElement | null>(null);
  const previous = useRef<string | null>(null);
  const [docked, setDocked] = useState<boolean | null>(null);
  const measureSlot = useCallback((slot: HTMLDivElement | null) => {
    const listNode = list.current;
    if (!listNode || !slot) return;
    // Flex wraps the inspector below the list when their two reading widths no longer fit.
    const measure = () => {
      setDocked(Math.abs(slot.offsetLeft - listNode.offsetLeft) > 1);
    };
    measure();
    const observer = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(measure);
    observer?.observe(listNode);
    observer?.observe(slot);
    return () => {
      observer?.disconnect();
      setDocked(null);
    };
  }, []);
  // The sheet releases inertness in passive cleanup before the list or dock can take focus.
  useEffect(() => {
    const old = previous.current;
    previous.current = selected;
    if (!selected && old) {
      const row = list.current?.querySelector<HTMLElement>(`[data-inspect-id="${old}"]`);
      const trigger = origin.current?.isConnected ? origin.current : (row ?? list.current);
      origin.current = null;
      const frame = requestAnimationFrame(() => trigger?.focus());
      return () => cancelAnimationFrame(frame);
    }
  }, [selected]);
  useEffect(() => {
    const target = heading.current;
    if (selected && docked && target) {
      if (target?.parentElement?.parentElement) target.parentElement.parentElement.scrollTop = 0;
      const frame = requestAnimationFrame(() => target.focus({ preventScroll: true }));
      return () => cancelAnimationFrame(frame);
    }
  }, [selected, docked]);
  return (
    <div className="inspection-workspace">
      <div
        ref={list}
        tabIndex={-1}
        className="inspection-list"
        onFocusCapture={(event) => {
          if (event.target instanceof HTMLElement && event.target.closest('[data-inspect-trigger]'))
            origin.current = event.target;
        }}
      >
        {children}
      </div>
      {selected && (
        <div ref={measureSlot} className="inspection-slot">
          {docked === null ? null : docked ? (
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
          )}
        </div>
      )}
    </div>
  );
}
