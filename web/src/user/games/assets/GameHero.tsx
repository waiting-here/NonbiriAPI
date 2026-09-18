import fishingHero from '@shared/assets/game-heroes/fishing.webp';
import linkLinkHero from '@shared/assets/game-heroes/linklink.webp';
import rpsHero from '@shared/assets/game-heroes/rps.webp';
import blackjackHero from '@shared/assets/game-heroes/blackjack.webp';
import biddingHero from '@shared/assets/game-heroes/bidding.webp';
import likesHero from '@shared/assets/game-heroes/likes.webp';

const heroSources = {
  fishing: fishingHero,
  linklink: linkLinkHero,
  rps: rpsHero,
  bidding: biddingHero,
  blackjack: blackjackHero,
  likes: likesHero,
} as const;

export type GameHeroKind = keyof typeof heroSources;

export function GameHero({ kind }: { kind: GameHeroKind }) {
  return (
    <img
      className="game-hero"
      src={heroSources[kind]}
      width={960}
      height={480}
      alt=""
      aria-hidden="true"
      decoding="async"
      loading={kind === 'fishing' ? 'eager' : 'lazy'}
      fetchPriority={kind === 'fishing' ? 'high' : 'auto'}
      draggable="false"
    />
  );
}
