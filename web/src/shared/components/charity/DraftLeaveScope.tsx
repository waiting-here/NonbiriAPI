import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
  type RefObject,
} from 'react';
import type { BlockerFunction } from 'react-router';
import { AsyncLeaveDialog } from '../AsyncLeaveDialog';

type Entry = {
  dirty: boolean;
  allow: RefObject<BlockerFunction | undefined>;
  stay: RefObject<(() => void) | undefined>;
};
const Context = createContext<((id: string, entry: Entry) => () => void) | null>(null);

/** One leave prompt coordinates drafts retained across the model and template views. */
export function DraftLeaveScope({
  children,
  onStay,
}: {
  children: ReactNode;
  onStay?: () => void;
}) {
  const parent = useContext(Context);
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
      <ScopeGuard
        parent={parent}
        dirty={active.length > 0}
        onStay={() => {
          onStay?.();
          active.forEach((entry) => entry.stay.current?.());
        }}
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
  return (
    <ScopeGuard parent={useContext(Context)} dirty={dirty} allowNavigation={allowNavigation} />
  );
}

function ScopeGuard({
  parent: register,
  dirty,
  allowNavigation,
  onStay,
}: {
  parent: React.ContextType<typeof Context>;
  dirty: boolean;
  allowNavigation?: BlockerFunction;
  onStay?: () => void;
}) {
  const stay = useRef(onStay);
  const id = useId();
  const allow = useRef(allowNavigation);
  useLayoutEffect(() => {
    // A save can navigate before the scope's registered dirty state updates.
    stay.current = onStay;
    allow.current = (navigation) => !dirty || Boolean(allowNavigation?.(navigation));
  }, [dirty, allowNavigation, onStay]);
  useEffect(
    () => (dirty ? register?.(id, { dirty, allow, stay }) : undefined),
    [register, id, dirty],
  );
  return register || !dirty ? null : (
    <AsyncLeaveDialog dirty={dirty} allowNavigation={allowNavigation} onStay={onStay} />
  );
}
