import { useDuelText } from './duel/copy';
import { GameBackLink } from './GameBackLink';
import { OnboardingCard } from './OnboardingCard';
import { GameWallets } from './GameWallets';
import type { GamesSnapshot } from './types';
import type { ButtonHTMLAttributes, ReactNode } from 'react';
import { Link } from 'react-router';
import { PageHeader } from '@shared/components/States';
import { useGameCopy } from '../copy';
import type { GameSoundControl } from './useGameSound';

export function GameHeader({
  game,
  wallets,
  sound,
  onRules,
  rankingsAvailable = false,
  children,
}: {
  readonly wallets?: Pick<GamesSnapshot, 'balance' | 'gameBalance' | 'onboarding'>;
  readonly game: 'fishing' | 'linklink' | 'rps';
  readonly sound: GameSoundControl;
  readonly onRules: () => void;
  readonly rankingsAvailable?: boolean;
  readonly children?: ReactNode;
}) {
  const { text } = useGameCopy();
  const duelText = useDuelText();
  return (
    <>
      <PageHeader
        back={<GameBackLink />}
        title={text(`${game}.eyebrow`)}
        actions={
          <>
            {wallets ? <GameWallets wallets={wallets} /> : null}
            <GameHeaderTool icon="?" label={text('common.rulesButton')} onClick={onRules} />
            <GameHeaderTool
              icon="♪"
              label={text(sound.enabled ? 'fishing.sound.on' : 'fishing.sound.off')}
              aria-pressed={sound.enabled}
              onClick={sound.toggle}
            />
            <Link
              className="btn btn-secondary game-header-tool"
              to="/credits"
              aria-label={text('presentation.creditHistory')}
              title={text('presentation.creditHistory')}
            >
              <span aria-hidden="true">◷</span>
            </Link>
            {rankingsAvailable ? (
              <a
                className="btn btn-secondary game-header-tool"
                href="#game-rankings"
                aria-label={duelText('ranking.leaderboards')}
                title={duelText('ranking.leaderboards')}
              >
                <span aria-hidden="true">▥</span>
              </a>
            ) : null}
            {children}
          </>
        }
      />
      {wallets ? <OnboardingCard game={game} progress={wallets.onboarding[game]} /> : null}
    </>
  );
}

export function GameHeaderTool({
  label,
  icon,
  ...props
}: { label: string; icon: ReactNode } & Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'children'>) {
  return (
    <button
      type="button"
      className="btn btn-secondary game-header-tool"
      aria-label={label}
      title={label}
      {...props}
    >
      <span aria-hidden="true">{icon}</span>
    </button>
  );
}
