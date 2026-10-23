import { useMemo, type ReactNode } from 'react';
import { usePublicConfig } from '@shared/query/publicConfig';
import { artworkCatalog } from './catalog';

import { ArtworkPolicyContext } from './context';
export function ArtworkPolicyProvider({
  disabled,
  catalog = artworkCatalog,
  children,
}: {
  disabled: boolean;
  catalog?: typeof artworkCatalog;
  children: ReactNode;
}) {
  const value = useMemo(() => ({ disabled, catalog }), [disabled, catalog]);
  return <ArtworkPolicyContext value={value}>{children}</ArtworkPolicyContext>;
}
export function ConfiguredArtworkPolicy({
  path = '/api/config',
  children,
}: {
  path?: string;
  children: ReactNode;
}) {
  const config = usePublicConfig(true, path);
  return (
    <ArtworkPolicyProvider
      disabled={config.isPending || !!config.error || config.data?.artworkR18Disabled !== false}
    >
      {children}
    </ArtworkPolicyProvider>
  );
}
