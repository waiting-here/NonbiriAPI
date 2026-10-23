import type { ImgHTMLAttributes } from 'react';
import { useTranslation } from 'react-i18next';
import { Icon } from '@shared/components/Icon';
import { useArtworkPolicy } from './context';
import './artwork.css';

export function ArtworkPlaceholder({
  name,
  rarity,
  className = '',
}: {
  name: string;
  rarity?: string;
  className?: string;
}) {
  const { t } = useTranslation();
  return (
    <span className={'nb-artwork-placeholder ' + className} role="img" aria-label={name}>
      <Icon name="spark" />
      <strong>{name}</strong>
      {rarity && <span>{rarity}</span>}
      <small>{t('artwork.unavailable')}</small>
    </span>
  );
}
export function ArtworkImage({
  artworkKey,
  fallbackSrc,
  ...props
}: ImgHTMLAttributes<HTMLImageElement> & {
  artworkKey: string | readonly string[];
  fallbackSrc?: string;
}) {
  const policy = useArtworkPolicy();
  const allowed =
    policy.allowed(artworkKey) &&
    (!props.src || policy.sourceAllowed(props.src)) &&
    (!props.srcSet ||
      props.srcSet
        .split(',')
        .every((source) => policy.sourceAllowed(source.trim().split(/\s+/)[0])));
  if (!allowed && !fallbackSrc)
    return <ArtworkPlaceholder name={props.alt ?? ''} className={props.className} />;
  return (
    <img
      {...props}
      src={allowed ? props.src : fallbackSrc}
      srcSet={allowed ? props.srcSet : undefined}
    />
  );
}
export function ArtworkBadge({ artworkKey }: { artworkKey: string | readonly string[] }) {
  const policy = useArtworkPolicy();
  return policy.rating(artworkKey) === 'r18' ? (
    <span className="nb-artwork-badge">R-18</span>
  ) : null;
}
export function ArtworkUnavailable({ artworkKey }: { artworkKey: string | readonly string[] }) {
  const policy = useArtworkPolicy();
  const { t } = useTranslation();
  return !policy.allowed(artworkKey) ? (
    <small className="nb-artwork-unavailable">{t('artwork.unavailable')}</small>
  ) : null;
}
