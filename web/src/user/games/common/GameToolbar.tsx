import { Link } from 'react-router';
import { useTranslation } from 'react-i18next';
import { Icon, type IconName } from '@shared/components/Icon';
import { Button } from '@shared/components/ui/Button';
import { MoreMenu } from '@shared/components/ui/MoreMenu';
import { useDuelText } from './duel/copy';

export interface ToolItem {
  id: string;
  label: string;
  icon: IconName;
  pressed?: boolean;
  disabled?: boolean;
  onClick?: () => void;
  to?: string;
  href?: string;
}

export interface GameToolbarAudio {
  enabled: boolean;
  toggle: () => void;
  labelOn?: string;
  labelOff?: string;
}

export interface GameToolbarProps {
  items: readonly ToolItem[];
  sound?: GameToolbarAudio;
  music?: GameToolbarAudio;
  audioUnavailable?: boolean;
  className?: string;
}

const ORDER = ['rules', 'sound', 'history', 'credits', 'rankings'];

function Tool({ item }: { item: ToolItem }) {
  const children = (
    <>
      <Icon name={item.icon} />
      <span className="game-toolbar__label">{item.label}</span>
    </>
  );
  const shared = {
    className: 'nb-btn nb-btn--secondary game-toolbar__button',
    'aria-label': item.label,
    title: item.label,
  };
  if (item.to || item.href) {
    const linkProps = {
      ...shared,
      'aria-disabled': item.disabled || undefined,
      tabIndex: item.disabled ? -1 : undefined,
      onClick: (event: React.MouseEvent) => {
        if (item.disabled) event.preventDefault();
        else item.onClick?.();
      },
      children,
    };
    return item.to ? <Link {...linkProps} to={item.to} /> : <a {...linkProps} href={item.href} />;
  }
  return (
    <Button
      {...shared}
      className="game-toolbar__button"
      type="button"
      disabled={item.disabled}
      aria-pressed={item.pressed}
      onClick={item.onClick}
    >
      {children}
    </Button>
  );
}

export function GameToolbar({
  items,
  sound,
  music,
  audioUnavailable,
  className,
}: GameToolbarProps) {
  const { t } = useTranslation();
  const text = useDuelText();
  const extra = items.filter((item) => !ORDER.includes(item.id));
  const soundLabel = t('user.games.presentation.soundControls');
  const moreLabel = t('user.games.presentation.moreTools');
  const audioItems = [
    ...(sound
      ? [
          {
            label: sound.enabled
              ? (sound.labelOn ?? text('common.soundOn'))
              : (sound.labelOff ?? text('common.soundOff')),
            icon: sound.enabled ? 'sound' : 'sound-off',
            control: sound,
          },
        ]
      : []),
    ...(music
      ? [
          {
            label: music.enabled
              ? (music.labelOn ?? text('common.musicOn'))
              : (music.labelOff ?? text('common.musicOff')),
            icon: 'music',
            control: music,
          },
        ]
      : []),
  ] as const;
  return (
    <div
      className={['game-toolbar', className].filter(Boolean).join(' ')}
      role="group"
      aria-label={t('user.games.presentation.tools')}
    >
      <div className="game-toolbar__items">
        {ORDER.map((id) => {
          if (id === 'sound' && audioItems.length)
            return (
              <MoreMenu
                key={id}
                label={soundLabel}
                triggerClassName="game-toolbar__button"
                trigger={
                  <>
                    <Icon name={sound?.enabled || music?.enabled ? 'sound' : 'sound-off'} />
                    <span className="game-toolbar__label">{soundLabel}</span>
                  </>
                }
                items={audioItems.map(({ label, icon, control }) => ({
                  label: (
                    <>
                      <Icon name={icon as IconName} />
                      <span>{label}</span>
                    </>
                  ),
                  ariaLabel: label,
                  title: label,
                  checked: control.enabled,
                  onSelect: control.toggle,
                }))}
              />
            );
          return items
            .filter((item) => item.id === id)
            .map((item) => <Tool key={item.id} item={item} />);
        })}
        {extra.length ? (
          <MoreMenu
            label={moreLabel}
            triggerClassName="game-toolbar__button"
            trigger={
              <>
                <Icon name="more" />
                <span className="game-toolbar__label">{moreLabel}</span>
              </>
            }
            items={extra.map((item) => ({
              label: (
                <>
                  <Icon name={item.icon} />
                  <span>{item.label}</span>
                </>
              ),
              ariaLabel: item.label,
              title: item.label,
              onSelect: item.onClick,
              disabled: item.disabled,
              checked: item.pressed,
              to: item.to,
              href: item.href,
            }))}
          />
        ) : null}
      </div>
      {audioUnavailable ? (
        <small role="status">{text('common.someAudioCouldNotLoadSwitchIt')}</small>
      ) : null}
    </div>
  );
}
