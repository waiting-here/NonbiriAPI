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
  return (
    <figure
      className={`likes-art ${className}`}
      data-art-slot={slot.key}
      data-placeholder={slot.placeholder}
      style={{ aspectRatio: slot.ratio.replace(':', ' / ') }}
    >
      <img
        src={slot.source}
        alt={label}
        loading="lazy"
        decoding="async"
        style={{ objectPosition: `${slot.focus[0] * 100}% ${slot.focus[1] * 100}%` }}
      />
    </figure>
  );
}
