import { createContext, useContext, useMemo } from 'react';
import { artworkCatalog } from './catalog';

export const ArtworkPolicyContext = createContext({ disabled: true, catalog: artworkCatalog });
export function useArtworkPolicy() {
  const { disabled, catalog } = useContext(ArtworkPolicyContext);
  return useMemo(
    () => ({
      disabled,
      momentKeys: catalog.momentKeys,
      rating: (keys: string | readonly string[]) =>
        (typeof keys === 'string' ? [keys] : keys).some((key) => catalog.rating(key) === 'r18')
          ? ('r18' as const)
          : ('general' as const),
      allowed: (keys: string | readonly string[]) => catalog.allowed(keys, disabled),
      sourceAllowed: (src: string) => catalog.sourceAllowed(src, disabled),
    }),
    [disabled, catalog],
  );
}
