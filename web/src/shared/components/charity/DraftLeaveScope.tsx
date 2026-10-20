import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type ReactNode,
  type RefObject,
} from 'react';
import type { BlockerFunction } from 'react-router';
import { AsyncLeaveDialog } from '../AsyncLeaveDialog';

type Entry = { dirty: boolean; allow: RefObject<BlockerFunction | undefined> };
const Context = createContext<((id: string, entry: Entry) => () => void) | null>(null);

/** One leave prompt coordinates drafts retained across the model and template views. */
export function DraftLeaveScope({
  children,
  onStay,
}: {
  children: ReactNode;
  onStay?: () => void;
}) {
  const [entries, setEntries] = useState<ReadonlyMap<string, Entry>>(() => new Map());
  const register = useCallback((id: string, entry: Entry) => {
    setEntries((previous) => new Map(previous).set(id, entry));
    return () =>
      setEntries((previous) => {
        const next = new Map(previous);
        next.delete(id);
        return next;
      });
  }, []);
  const active = useMemo(() => [...entries.values()].filter((entry) => entry.dirty), [entries]);
  return (
    <Context.Provider value={register}>
      <AsyncLeaveDialog
        dirty={active.length > 0}
        onStay={onStay}
        allowNavigation={(navigation) => active.every((entry) => entry.allow.current?.(navigation))}
      />
      {children}
    </Context.Provider>
  );
}

export function DraftLeaveGuard({
  dirty,
  allowNavigation,
}: {
  dirty: boolean;
  allowNavigation?: BlockerFunction;
}) {
  const register = useContext(Context);
  const id = useId();
  const allow = useRef(allowNavigation);
  useEffect(() => {
    allow.current = allowNavigation;
  }, [allowNavigation]);
  useEffect(() => register?.(id, { dirty, allow }), [register, id, dirty]);
  return register ? null : <AsyncLeaveDialog dirty={dirty} allowNavigation={allowNavigation} />;
}
