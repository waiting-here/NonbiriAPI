import { useDuelText } from './duel/copy';
import { GameBackLink } from './GameBackLink';
import { OnboardingCard } from './OnboardingCard';
import { GameWallets } from './GameWallets';
import type { GamesSnapshot } from './types';
import type { ButtonHTMLAttributes, ReactNode } from 'react';
import { GameToolbar, type ToolItem } from './GameToolbar';
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
  tools = [],
}: {
  readonly wallets?: Pick<GamesSnapshot, 'balance' | 'gameBalance' | 'onboarding'>;
  readonly game: 'fishing' | 'linklink' | 'rps';
  readonly sound: GameSoundControl;
  readonly onRules: () => void;
  readonly rankingsAvailable?: boolean;
  readonly children?: ReactNode;
  readonly tools?: readonly ToolItem[];
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
            <GameToolbar
              sound={{
                ...sound,
                labelOn: text('fishing.sound.on'),
                labelOff: text('fishing.sound.off'),
              }}
              items={[
                { id: 'rules', label: text('common.rulesButton'), icon: 'help', onClick: onRules },
                {
                  id: 'credits',
                  label: text('presentation.creditHistory'),
                  icon: 'credits',
                  to: '/credits',
                },
                ...(rankingsAvailable
                  ? [
                      {
                        id: 'rankings',
                        label: duelText('ranking.leaderboards'),
                        icon: 'trophy' as const,
                        href: '#game-rankings',
                      },
                    ]
                  : []),
                ...tools,
              ]}
            />
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
      className="nb-btn nb-btn--secondary game-header-tool"
      aria-label={label}
      title={label}
      {...props}
    >
      <span aria-hidden="true">{icon}</span>
    </button>
  );
}
