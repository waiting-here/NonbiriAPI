import { useState } from 'react';
import mark from '@shared/assets/nonbiri-mark.svg';
import type { ArtSlot } from './art';

export function LikesArt({
  slot,
  label,
  className = '',
}: {
  readonly slot: ArtSlot;
  readonly label: string;
  readonly className?: string;
}) {
  const [failedSource, setFailedSource] = useState<string | null>(null);
  const showingFallback = failedSource === slot.source;
  return (
    <figure
      className={`likes-art ${className}`}
      data-art-slot={slot.key}
      data-placeholder={slot.placeholder}
      data-fallback={showingFallback}
      style={{ aspectRatio: slot.ratio.replace(':', ' / ') }}
    >
      <img
        src={showingFallback ? mark : slot.source}
        alt={label}
        loading="lazy"
        decoding="async"
        onError={showingFallback ? undefined : () => setFailedSource(slot.source)}
        style={{ objectPosition: `${slot.focus[0] * 100}% ${slot.focus[1] * 100}%` }}
      />
    </figure>
  );
}
