import { lazy, Suspense, useCallback } from 'react';
import { ErrorState, LoadingState } from '@shared/components/States';
import { useGamesSnapshot } from '../snapshot';
import { isMaintenance } from '../request';
import { GameBackLink } from '../GameBackLink';
import type { DuelGame, DuelConfig, DuelLobbyContext } from './types';
import '../../games.css';

const Bidding = lazy(async () => ({
  default: (await import('../../bidding/BiddingGame')).BiddingGame,
}));
const Likes = lazy(async () => ({ default: (await import('../../likes/LikesGame')).LikesGame }));
const Gwent = lazy(async () => ({ default: (await import('../../gwent/GwentGame')).GwentGame }));
const unavailable: DuelConfig = { enabled: false, available: false, modes: {} };

function DuelPage({ game }: { readonly game: DuelGame }) {
  const snapshot = useGamesSnapshot();
  const { refetch } = snapshot;
  const refreshWallets = useCallback(() => refetch(), [refetch]);
  if (snapshot.isPending)
    return (
      <main className="game-page">
        <GameBackLink />
        <LoadingState />
      </main>
    );
  if (snapshot.error && !snapshot.data && !isMaintenance(snapshot.error))
    return (
      <main className="game-page">
        <GameBackLink />
        <ErrorState error={snapshot.error} onRetry={refreshWallets} />
      </main>
    );
  const context: DuelLobbyContext = {
    config: snapshot.data?.[game] ?? unavailable,
    wallets: snapshot.data ?? { balance: '0', gameBalance: '0' },
    onboarding: game === 'gwent' ? undefined : snapshot.data?.onboarding[game],
    accepting: !!snapshot.data?.gamesEnabled && !snapshot.error,
    refreshWallets,
  };
  return (
    <main className="game-page">
      <Suspense
        fallback={
          <>
            <GameBackLink />
            <LoadingState />
          </>
        }
      >
        {game === 'gwent' ? (
          <Gwent {...context} />
        ) : game === 'bidding' ? (
          <Bidding {...context} />
        ) : (
          <Likes {...context} />
        )}
      </Suspense>
    </main>
  );
}
export function BiddingPage() {
  return <DuelPage game="bidding" />;
}
export function LikesPage() {
  return <DuelPage game="likes" />;
}

export function GwentPage() {
  return <DuelPage game="gwent" />;
}
