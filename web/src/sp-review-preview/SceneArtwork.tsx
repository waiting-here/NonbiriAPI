import { Artwork } from '../user/activities/gacha/Artwork';
import { artSourceSet } from '../user/activities/gacha/art-resolution';
import { TomorrowArtwork } from '../user/activities/gacha/tomorrow/Artwork';
import { entryName, type Entry, type PreviewVariant } from './fixture';

export function SceneArtwork({
  entry,
  variant,
  dark,
  animated,
  paused,
  english,
  className,
  highResolution,
}: {
  entry: Entry;
  variant: PreviewVariant;
  dark: boolean;
  animated: boolean;
  paused: boolean;
  english: boolean;
  className?: string;
  highResolution?: boolean;
}) {
  if (entry.kind === 'tomorrow')
    return (
      <TomorrowArtwork
        refined={entry.refined}
        variant={variant}
        dark={dark}
        animated={animated}
        paused={paused}
        className={className}
        highResolution={highResolution}
      />
    );
  if (variant === 'card')
    return (
      <span className={className}>
        <Artwork card={entry.card} animated={animated} full paused={paused} />
      </span>
    );
  const src = variant === 'cover' ? entry.cover : entry.illustration;
  return (
    <img
      className={className}
      src={src}
      srcSet={artSourceSet(src)}
      sizes="(width < 64rem) 94vw, 46vw"
      alt={entryName(entry, english)}
    />
  );
}
