import { LakeCover } from '../../activities/lake-notes/LakeCover';
import fishingHero from '@shared/assets/game-heroes/fishing.webp';
import linkLinkHero from '@shared/assets/game-heroes/linklink.webp';
import rpsHero from '@shared/assets/game-heroes/rps.webp';
import blackjackHero from '@shared/assets/game-heroes/blackjack.webp';
import biddingHero from '@shared/assets/game-heroes/bidding.webp';
import likesHero from '@shared/assets/game-heroes/likes.webp';

import catchHero from '@shared/assets/game-heroes/steadycatch.webp';

const heroSources = {
  whalehaven: '/assets/whale-haven/backgrounds/cabin-desktop.webp',
  lakenotes: '',
  steadycatch: catchHero,
  fishing: fishingHero,
  linklink: linkLinkHero,
  rps: rpsHero,
  bidding: biddingHero,
  blackjack: blackjackHero,
  likes: likesHero,
  gwent: '',
} as const;

export type GameHeroKind = keyof typeof heroSources;

export function GameHero({ kind, eager = false }: { kind: GameHeroKind; eager?: boolean }) {
  if (kind === 'whalehaven')
    return (
      <div className="game-whale-cover" aria-hidden="true">
        <img alt="" loading="lazy" src={heroSources.whalehaven} />
        <img alt="" loading="lazy" src="/assets/whale-haven/sprites/whale-cheerful.webp" />
      </div>
    );
  if (kind === 'lakenotes') return <LakeCover />;
  if (kind === 'gwent')
    return (
      <div className="game-gwent-hero" aria-hidden="true">
        {['openai', 'deepseek', 'claude', 'gemini'].map((faction) => (
          <img
            key={faction}
            src={`/assets/gwent/cards/${faction}_leader-cg-v1.webp`}
            alt=""
            loading={eager ? 'eager' : 'lazy'}
            decoding="async"
          />
        ))}
        <span>AI GWENT</span>
      </div>
    );
  return (
    <img
      className="game-hero"
      src={heroSources[kind]}
      width={960}
      height={480}
      alt=""
      aria-hidden="true"
      decoding="async"
      loading={eager || kind === 'fishing' ? 'eager' : 'lazy'}
      fetchPriority={kind === 'fishing' ? 'high' : 'auto'}
      draggable="false"
    />
  );
}
