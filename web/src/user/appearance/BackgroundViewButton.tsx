import { lazy, Suspense } from 'react';
import { useOptionalAppearance } from './context';

const Viewer = lazy(() =>
  import('./BackgroundViewer').then((module) => ({ default: module.BackgroundViewButton })),
);
export function BackgroundViewButton(
  props: { requestedScene?: string; onReturn?: () => void } = {},
) {
  const state = useOptionalAppearance()?.state;
  return state && state.selected !== 'default' && state.unlocked.includes(state.selected) ? (
    <Suspense fallback={null}>
      <Viewer {...props} />
    </Suspense>
  ) : null;
}
