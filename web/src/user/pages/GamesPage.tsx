import { UserPageGate } from '../components/UserPageGate';
import { gameRegistry, resolveGameRegistration } from '../games/registry';

/** User games shell. */
export function GamesPage() {
  const registration = resolveGameRegistration(gameRegistry, 'fishing', 1);
  if (!registration) throw new Error('Fishing game registration is unavailable.');
  const Game = registration.page;
  return (
    <UserPageGate>
      <Game />
    </UserPageGate>
  );
}
