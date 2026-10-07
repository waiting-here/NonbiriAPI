import { Link } from 'react-router';
import { useGameCopy } from '../copy';

export function GameBackLink({ className = 'game-back-link' }: { className?: string }) {
  const { text } = useGameCopy();
  return (
    <Link className={className} to="/games">
      {text('common.back')}
    </Link>
  );
}
