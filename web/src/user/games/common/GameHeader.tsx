import type { ReactNode } from 'react';
import { Link } from 'react-router';
import { PageHeader } from '@shared/components/States';
import { useGameCopy } from '../copy';
import { GameRulesButton } from './GameRulesDialog';
import { GameSoundButton } from './GameSoundButton';
import type { GameSoundControl } from './useGameSound';

export function GameHeader({
  game,
  sound,
  onRules,
  compact = false,
  children,
}: {
  readonly game: 'fishing' | 'linklink' | 'rps';
  readonly sound: GameSoundControl;
  readonly onRules: () => void;
  readonly compact?: boolean;
  readonly children?: ReactNode;
}) {
  const { text } = useGameCopy();
  return (
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
          <GameRulesButton label={text('common.rulesButton')} onClick={onRules} />
          <GameSoundButton sound={sound} />
          {children}
        </>
      }
    />
  );
}
