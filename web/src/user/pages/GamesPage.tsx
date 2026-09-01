import { useLocation } from 'react-router';
import { UserPageGate } from '../components/UserPageGate';
import { GameCenter } from '../games/GameCenter';
import { gameRegistry, resolveGameRegistration } from '../games/registry';

/** User games shell. */
export function GamesPage() {
  const location = useLocation();
  const path = location.pathname.replace(/\/+$/, '') || '/';
  const id =
    path === '/games'
      ? null
      : path === '/games/fishing'
        ? 'fishing'
        : path === '/games/linklink'
          ? 'linklink'
          : path === '/games/rps'
            ? 'rps'
            : null;
  const registration = id ? resolveGameRegistration(gameRegistry, id, 1) : null;
  if (id && !registration) throw new Error('Game registration is unavailable.');
  const Game = registration?.page;
  return <UserPageGate>{Game ? <Game /> : <GameCenter />}</UserPageGate>;
}
