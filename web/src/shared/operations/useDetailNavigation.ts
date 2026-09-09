import { useCallback, useEffect, useRef } from 'react';

/** Keep inline details and their originating list reachable after navigation. */
export function useDetailNavigation<
  ListElement extends HTMLElement = HTMLDivElement,
  DetailElement extends HTMLElement = HTMLDivElement,
>(selection: string) {
  const listNode = useRef<ListElement | null>(null);
  const detailNode = useRef<DetailElement | null>(null);
  const previousSelection = useRef('');
  const returnTarget = useRef<HTMLElement | null>(null);

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
    if (previous === selection) return;
    previousSelection.current = selection;
    if (selection) {
      remember();
      detailNode.current?.focus({ preventScroll: true });
      detailNode.current?.scrollIntoView?.({ block: 'start', behavior: 'instant' });
    } else if (previous) {
      const target = returnTarget.current?.isConnected ? returnTarget.current : listNode.current;
      target?.focus({ preventScroll: true });
      target?.scrollIntoView?.({ block: 'nearest', behavior: 'instant' });
      returnTarget.current = null;
    }
  }, [selection, remember]);

  return { listRef, detailRef, remember };
}
