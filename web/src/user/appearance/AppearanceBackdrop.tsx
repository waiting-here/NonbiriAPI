import { lazy, Suspense } from 'react';
import { eligibleMoment, useOptionalAppearance } from './context';

const Scene = lazy(() =>
  import('./SceneBackdrop').then((module) => ({ default: module.AppearanceBackdrop })),
);

export function ActiveAppearanceBackdrop() {
  const state = useOptionalAppearance()?.state;
  return state?.unlocked.includes(state.selected) ? (
    <AppearanceBackdrop selected={state.selected} moment={eligibleMoment(state)} />
  ) : null;
}

export function AppearanceBackdrop(props: { selected: string; moment?: string }) {
  return props.selected === 'default' ? null : (
    <Suspense fallback={null}>
      <Scene {...props} />
    </Suspense>
  );
}
