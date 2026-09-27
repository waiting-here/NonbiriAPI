import { useReducer } from 'react';

interface History<T> { past: T[]; present: T; future: T[] }
type Action<T> = { type: 'commit' | 'reset'; value: T } | { type: 'undo' | 'redo' };
function reduce<T>(state: History<T>, action: Action<T>): History<T> {
  switch (action.type) {
    case 'reset': return { past: [], present: action.value, future: [] };
    case 'commit': return { past: [...state.past.slice(-49), state.present], present: action.value, future: [] };
    case 'undo': return state.past.length ? { past: state.past.slice(0, -1), present: state.past[state.past.length - 1], future: [state.present, ...state.future] } : state;
    case 'redo': return state.future.length ? { past: [...state.past, state.present], present: state.future[0], future: state.future.slice(1) } : state;
  }
}
export function useHistory<T>(initial: T) {
  const [state, dispatch] = useReducer(reduce<T>, { past: [], present: initial, future: [] });
  return { state, commit: (value: T) => dispatch({ type: 'commit', value }), reset: (value: T) => dispatch({ type: 'reset', value }), undo: () => dispatch({ type: 'undo' }), redo: () => dispatch({ type: 'redo' }) };
}
