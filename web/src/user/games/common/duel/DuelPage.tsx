import { lazy, Suspense, useCallback } from 'react';
import { ErrorState, LoadingState } from '@shared/components/States';
import { useGamesSnapshot } from '../snapshot';
import { isMaintenance } from '../request';
import type { DuelGame, DuelConfig, DuelLobbyContext } from './types';
import '../../games.css';

const Bidding = lazy(async () => ({
  default: (await import('../../bidding/BiddingGame')).BiddingGame,
}));
const Likes = lazy(async () => ({ default: (await import('../../likes/LikesGame')).LikesGame }));
const unavailable: DuelConfig = { enabled: false, available: false, modes: {} };

function DuelPage({ game }: { readonly game: DuelGame }) {
  const snapshot = useGamesSnapshot();
  const { refetch } = snapshot;
  const refreshWallets = useCallback(() => refetch(), [refetch]);
  if (snapshot.isPending) return <LoadingState />;
  if (snapshot.error && !snapshot.data && !isMaintenance(snapshot.error))
    return <ErrorState error={snapshot.error} onRetry={refreshWallets} />;
  const context: DuelLobbyContext = {
    config: snapshot.data?.[game] ?? unavailable,
    wallets: snapshot.data ?? { balance: '0', gameBalance: '0' },
    onboarding: snapshot.data?.onboarding[game],
    accepting: !!snapshot.data?.gamesEnabled && !snapshot.error,
    refreshWallets,
  };
  return (
    <main className="game-page">
      <Suspense fallback={<LoadingState />}>
        {game === 'bidding' ? <Bidding {...context} /> : <Likes {...context} />}
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
