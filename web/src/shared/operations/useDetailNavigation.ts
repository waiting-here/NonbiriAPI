import { useCallback, useEffect, useRef } from 'react';

/** Keep inline details and their originating list reachable after navigation. */
export function useDetailNavigation<
  ListElement extends HTMLElement = HTMLDivElement,
  DetailElement extends HTMLElement = HTMLDivElement,
>(selection: string, ready = true) {
  const listNode = useRef<ListElement | null>(null);
  const detailNode = useRef<DetailElement | null>(null);
  const previousSelection = useRef('');
  const returnTarget = useRef<HTMLElement | null>(null);
  const pendingNavigation = useRef<string | null>(null);
  const pendingFocus = useRef<HTMLElement | null>(null);

  const listRef = useCallback((node: ListElement | null) => {
    listNode.current = node;
  }, []);
  const detailRef = useCallback((node: DetailElement | null) => {
    detailNode.current = node;
  }, []);

  const remember = useCallback(() => {
    const active = document.activeElement;
    if (
      active instanceof HTMLElement &&
      listNode.current?.contains(active) &&
      !detailNode.current?.contains(active)
    )
      returnTarget.current = active;
  }, []);

  useEffect(() => {
    const previous = previousSelection.current;
    if (previous !== selection) {
      previousSelection.current = selection;
      const active = document.activeElement;
      pendingFocus.current =
        active instanceof HTMLElement && active !== document.body ? active : null;
      if (selection) {
        remember();
        pendingNavigation.current = selection;
      } else if (previous) {
        pendingNavigation.current = '';
      }
    }

    // The detail shell mounts before its list and detail queries have settled.
    // Keep the navigation pending until the caller reports that the layout is
    // complete, so a later list response cannot move the focused target away.
    if (!ready || pendingNavigation.current !== selection) return;
    const active = document.activeElement;
    const currentFocus =
      active instanceof HTMLElement && active !== document.body && active.isConnected
        ? active
        : null;
    if (
      (pendingFocus.current?.isConnected && currentFocus !== pendingFocus.current) ||
      (!pendingFocus.current?.isConnected && currentFocus)
    ) {
      pendingNavigation.current = null;
      pendingFocus.current = null;
      return;
    }
    if (selection) {
      const target = detailNode.current;
      if (!target) return;
      target.focus({ preventScroll: true });
      target.scrollIntoView?.({ block: 'start', behavior: 'instant' });
    } else {
      const target = returnTarget.current?.isConnected ? returnTarget.current : listNode.current;
      if (!target) return;
      target.focus({ preventScroll: true });
      target.scrollIntoView?.({ block: 'nearest', behavior: 'instant' });
      returnTarget.current = null;
    }
    pendingNavigation.current = null;
    pendingFocus.current = null;
  }, [ready, selection, remember]);

  return { listRef, detailRef, remember };
}
