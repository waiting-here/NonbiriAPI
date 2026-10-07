import { createElement, lazy, Suspense, type ComponentType } from 'react';
import { GameBackLink } from './common/GameBackLink';
import { LoadingState } from '@shared/components/States';
import { BiddingPage, LikesPage, GwentPage } from './common/duel/DuelPage';
function GameLoading() {
  return createElement(
    'main',
    { className: 'game-page' },
    createElement(GameBackLink),
    createElement(LoadingState),
  );
}
const Catch = lazy(async () => ({
  default: (await import('./steady-catch/SteadyCatchGame')).SteadyCatchGame,
}));
function CatchPage() {
  return createElement(Suspense, { fallback: createElement(GameLoading) }, createElement(Catch));
}
const Lake = lazy(async () => ({
  default: (await import('../activities/lake-notes/LakeNotesPage')).LakeNotesPage,
}));
function LakePage() {
  return createElement(Suspense, { fallback: createElement(GameLoading) }, createElement(Lake));
}
const Fishing = lazy(async () => ({
  default: (await import('./fishing/FishingGame')).FishingGame,
}));
const LinkLink = lazy(async () => ({
  default: (await import('./linklink/LinkLinkGame')).LinkLinkGame,
}));
const RPS = lazy(async () => ({ default: (await import('./rps/RPSGame')).RPSGame }));
const Blackjack = lazy(async () => ({
  default: (await import('./blackjack/BlackjackGame')).BlackjackGame,
}));
function FishingPage() {
  return createElement(Suspense, { fallback: createElement(GameLoading) }, createElement(Fishing));
}
function LinkLinkPage() {
  return createElement(Suspense, { fallback: createElement(GameLoading) }, createElement(LinkLink));
}
function RPSPage() {
  return createElement(Suspense, { fallback: createElement(GameLoading) }, createElement(RPS));
}
function BlackjackPage() {
  return createElement(
    Suspense,
    { fallback: createElement(GameLoading) },
    createElement(Blackjack),
  );
}

/**
 * The user station keeps game registration separate from connector
 * registration.  A registration only describes the route-facing shell; all
 * economic and outcome decisions remain in the server game service.
 */
export interface GameRegistration {
  readonly id: string;
  readonly path?: string;
  readonly version: number;
  readonly titleKey: string;
  readonly page: ComponentType;
}

const registrationKey = (id: string, version: number) => `${id}\u0000${version}`;

/** Resolve a route-facing registration only after validating the registry key space. */
export function resolveGameRegistration(
  registry: readonly GameRegistration[],
  id: string,
  version: number,
): GameRegistration | null {
  const seen = new Set<string>();
  for (const entry of registry) {
    const key = registrationKey(entry.id, entry.version);
    if (seen.has(key)) throw new Error(`Duplicate game registration: ${entry.id}@${entry.version}`);
    seen.add(key);
  }
  return registry.find((entry) => entry.id === id && entry.version === version) ?? null;
}

export const gameRegistry: readonly GameRegistration[] = Object.freeze([
  {
    id: 'fishing',
    version: 1,
    titleKey: 'games.fishing.title',
    page: FishingPage,
  },
  {
    id: 'linklink',
    version: 1,
    titleKey: 'games.linklink.title',
    page: LinkLinkPage,
  },
  {
    id: 'rps',
    version: 1,
    titleKey: 'games.rps.title',
    page: RPSPage,
  },
  { id: 'bidding', version: 1, titleKey: 'games.bidding.title', page: BiddingPage },
  { id: 'likes', version: 1, titleKey: 'games.likes.title', page: LikesPage },
  { id: 'gwent', version: 1, titleKey: 'games.gwent.title', page: GwentPage },
  {
    id: 'steadycatch',
    path: '/games/steady-catch',
    version: 1,
    titleKey: 'games.steadycatch.title',
    page: CatchPage,
  },
  { id: 'blackjack', version: 1, titleKey: 'games.blackjack.title', page: BlackjackPage },
  {
    id: 'lakenotes',
    path: '/games/lake-notes',
    version: 1,
    titleKey: 'games.lakenotes.title',
    page: LakePage,
  },
]);
