import { lazy } from 'react';

export const LazyIndependentDiagnostics = lazy(() =>
  import('./IndependentDiagnostics').then(({ IndependentDiagnostics }) => ({
    default: IndependentDiagnostics,
  })),
);
