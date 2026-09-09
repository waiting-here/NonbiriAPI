import { useSyncExternalStore, type CSSProperties } from 'react';

const query = '(max-width: 820px)';
function subscribeSize(changed: () => void) {
  const media = window.matchMedia?.(query);
  media?.addEventListener('change', changed);
  return () => media?.removeEventListener('change', changed);
}

export function Atmosphere({ count }: { readonly count: string | null }) {
  const small = useSyncExternalStore(
    subscribeSize,
    () => window.matchMedia?.(query).matches ?? false,
    () => true,
  );
  const ties = count === null ? 0n : BigInt(count);
  const level = ties >= 5n ? 3 : ties >= 3n ? 2 : ties > 0n ? 1 : 0;
  const particles = (small ? [0, 4, 8, 12] : [0, 8, 20, 32])[level];
  return (
    <div className="rps-atmosphere" data-level={level} aria-hidden="true">
      {Array.from({ length: particles }, (_, index) => (
        <i
          key={index}
          style={
            {
              '--particle-x': `${(index * 37 + 11) % 100}%`,
              '--particle-y': `${(index * 29 + 7) % 100}%`,
              '--particle-delay': `${-(index % 7)}s`,
            } as CSSProperties
          }
        />
      ))}
    </div>
  );
}
