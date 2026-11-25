import { useEffect, type ReactNode } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { ErrorState, LoadingState } from '@shared/components/States';
import { hasFeature, loadFeature, observeFeature, type FeatureCatalog } from './featureCatalog';

export function FeatureCopy({
  catalog,
  children,
}: {
  catalog: FeatureCatalog;
  children: ReactNode;
}) {
  const { i18n } = useTranslation();
  const language = (i18n.resolvedLanguage ?? i18n.language).startsWith('zh') ? 'zh' : 'en';
  useEffect(() => observeFeature(i18n, catalog), [i18n, catalog]);
  const ready = hasFeature(i18n, catalog, language);
  const { status, error, refetch } = useQuery({
    queryKey: ['feature-copy', catalog.path, language],
    queryFn: async () => {
      await loadFeature(i18n, catalog, language);
      return true;
    },
    enabled: !ready,
    retry: false,
    notifyOnChangeProps: ['status', 'error', 'dataUpdatedAt'],
  });
  if (ready) return children;
  if (status === 'error') return <ErrorState error={error} onRetry={() => void refetch()} />;
  return <LoadingState />;
}
