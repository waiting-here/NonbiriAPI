import { useReducer } from 'react';

export interface History<T> { past: T[]; present: T; future: T[] }
type Action<T> = { type: 'commit' | 'reset'; value: T } | { type: 'undo' | 'redo' };
const MAX_ENTRIES = 100;
const MAX_BYTES = 8 * 1024 * 1024;
const bytes = (value: unknown) => new TextEncoder().encode(JSON.stringify(value)).byteLength;
function bounded<T>(state: History<T>): History<T> {
  const past = [...state.past], future = [...state.future];
  let total = [...past, state.present, ...future].reduce((sum, value) => sum + bytes(value), 0);
  while (past.length + future.length > MAX_ENTRIES || total > MAX_BYTES) {
    if (past.length) total -= bytes(past.shift());
    else if (future.length) total -= bytes(future.pop());
    else break;
  }
  return { past, present: state.present, future };
}
export function historyReducer<T>(state: History<T>, action: Action<T>): History<T> {
  switch (action.type) {
    case 'reset': return { past: [], present: action.value, future: [] };
    case 'commit':
      if (JSON.stringify(state.present) === JSON.stringify(action.value)) return state;
      return bounded({ past: [...state.past, state.present], present: action.value, future: [] });
    case 'undo': return state.past.length ? bounded({ past: state.past.slice(0, -1), present: state.past[state.past.length - 1], future: [state.present, ...state.future] }) : state;
    case 'redo': return state.future.length ? bounded({ past: [...state.past, state.present], present: state.future[0], future: state.future.slice(1) }) : state;
  }
}
export function useHistory<T>(initial: T) {
  const [state, dispatch] = useReducer(historyReducer<T>, { past: [], present: initial, future: [] });
  return { state, commit: (value: T) => dispatch({ type: 'commit', value }), reset: (value: T) => dispatch({ type: 'reset', value }), undo: () => dispatch({ type: 'undo' }), redo: () => dispatch({ type: 'redo' }) };
}
