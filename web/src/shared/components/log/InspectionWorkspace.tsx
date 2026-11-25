import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
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
  // A stable portal target keeps the same editors, open folds and loaded metadata in both layouts.
  const [detailHost] = useState(() => document.createElement('div'));
  const [mount, setMount] = useState<HTMLDivElement | null>(null);
  const layout = useRef<boolean | null>(null);
  const transfer = useRef<{ selected: string; top: number; focus: HTMLElement | null } | null>(
    null,
  );
  const attachDetail = useCallback(
    (node: HTMLDivElement | null) => {
      if (!node) return;
      node.appendChild(detailHost);
      setMount(node);
      return () => setMount(null);
    },
    [detailHost],
  );
  const origin = useRef<HTMLElement | null>(null);
  const previous = useRef<string | null>(null);
  const [docked, setDocked] = useState<boolean | null>(null);
  const measureSlot = useCallback(
    (slot: HTMLDivElement | null) => {
      const listNode = list.current;
      if (!listNode || !slot) return;
      // Flex wraps the inspector below the list when their two reading widths no longer fit.
      const measure = () => {
        const next = Math.abs(slot.offsetLeft - listNode.offsetLeft) > 1;
        if (next === layout.current) return;
        const oldMount = detailHost.parentElement;
        const oldScroll = oldMount?.closest<HTMLElement>('.inspection-dock, .nb-sheet__body');
        if (layout.current !== null && oldMount?.dataset.record && oldScroll) {
          const active = document.activeElement;
          transfer.current = {
            selected: oldMount.dataset.record,
            top: oldScroll.scrollTop,
            focus: active instanceof HTMLElement && detailHost.contains(active) ? active : null,
          };
        }
        layout.current = next;
        setDocked(next);
      };
      measure();
      const observer = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(measure);
      observer?.observe(listNode);
      observer?.observe(slot);
      return () => {
        observer?.disconnect();
        layout.current = null;
        transfer.current = null;
        setDocked(null);
      };
    },
    [detailHost],
  );
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
    if (!selected || !mount?.isConnected || detailHost.parentElement !== mount) return;
    const prior = transfer.current?.selected === selected ? transfer.current : null;
    transfer.current = null;
    const viewport = mount.closest<HTMLElement>('.inspection-dock, .nb-sheet__body');
    const title = mount
      .closest('.inspection-dock, [role="dialog"]')
      ?.querySelector<HTMLElement>('h2');
    const frame = requestAnimationFrame(() => {
      // A cached detail can focus its linked attempt before the inspector mounts.
      if (!prior && detailHost.contains(document.activeElement)) return;
      if (viewport) viewport.scrollTop = prior?.top ?? 0;
      (prior?.focus?.isConnected ? prior.focus : title)?.focus({ preventScroll: true });
    });
    return () => cancelAnimationFrame(frame);
  }, [selected, mount, detailHost]);
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
        <div
          ref={measureSlot}
          className="inspection-slot"
          onKeyDown={(event) => {
            const target = event.target;
            if (
              event.key === 'Escape' &&
              !event.defaultPrevented &&
              target instanceof Node &&
              (detailHost.contains(target) || event.currentTarget.contains(target))
            ) {
              event.preventDefault();
              event.stopPropagation();
              onClose();
            }
          }}
        >
          {docked === null ? null : docked ? (
            <section className="inspection-dock" aria-label={title}>
              <header className="inspection-head">
                <h2 tabIndex={-1}>{title}</h2>
                <Button type="button" onClick={onClose}>
                  {closeLabel}
                </Button>
              </header>
              <div ref={attachDetail} data-record={selected} />
            </section>
          ) : (
            <Sheet open title={title} size="lg" closeLabel={closeLabel} onClose={onClose}>
              <div ref={attachDetail} data-record={selected} />
            </Sheet>
          )}
          {docked !== null && createPortal(detail, detailHost, selected)}
        </div>
      )}
    </div>
  );
}
