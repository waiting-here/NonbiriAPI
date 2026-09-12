import { OnboardingCard } from './OnboardingCard';
import { GameWallets } from './GameWallets';
import type { GamesSnapshot } from './types';
import type { ReactNode } from 'react';
import { Link } from 'react-router';
import { PageHeader } from '@shared/components/States';
import { useGameCopy } from '../copy';
import { GameRulesButton } from './GameRulesDialog';
import { GameSoundButton } from './GameSoundButton';
import type { GameSoundControl } from './useGameSound';

export function GameHeader({
  game,
  wallets,
  sound,
  onRules,
  compact = false,
  children,
}: {
  readonly wallets?: Pick<GamesSnapshot, 'balance' | 'gameBalance' | 'onboarding'>;
  readonly game: 'fishing' | 'linklink' | 'rps';
  readonly sound: GameSoundControl;
  readonly onRules: () => void;
  readonly compact?: boolean;
  readonly children?: ReactNode;
}) {
  const { text } = useGameCopy();
  return (
    <>
      <PageHeader
        back={
          <Link className="game-back-link" to="/games">
            {text('common.back')}
          </Link>
        }
        eyebrow={compact ? undefined : text(`${game}.eyebrow`)}
        title={text(`${game}.${compact ? 'eyebrow' : 'title'}`)}
        description={compact ? undefined : text(`${game}.description`)}
        actions={
          <>
            {wallets ? <GameWallets wallets={wallets} /> : null}
            <GameRulesButton label={text('common.rulesButton')} onClick={onRules} />
            <GameSoundButton sound={sound} />
            {children}
          </>
        }
      />
      {wallets ? <OnboardingCard game={game} progress={wallets.onboarding[game]} /> : null}
    </>
  );
}
