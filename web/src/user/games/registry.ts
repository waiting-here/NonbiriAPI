import type { ComponentType } from 'react';
import { FishingGame } from './fishing/FishingGame';
import { LinkLinkGame } from './linklink/LinkLinkGame';
import { RPSGame } from './rps/RPSGame';

/**
 * The user station keeps game registration separate from connector
 * registration.  A registration only describes the route-facing shell; all
 * economic and outcome decisions remain in the server game service.
 */
export interface GameRegistration {
  readonly id: string;
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
    page: FishingGame,
  },
  {
    id: 'linklink',
    version: 1,
    titleKey: 'games.linklink.title',
    page: LinkLinkGame,
  },
  {
    id: 'rps',
    version: 1,
    titleKey: 'games.rps.title',
    page: RPSGame,
  },
]);
