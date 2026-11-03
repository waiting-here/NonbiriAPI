import { Dialog } from 'react-aria-components';
import { AnchoredSurface, FirstActionFocus } from './OverlaySurface';
import { Button } from '@shared/components/ui/Button';
import { useId, useRef, useState, type ReactNode } from 'react';
import { Link } from 'react-router';
import { useTranslation } from 'react-i18next';
import { Icon } from './Icon';
import { ThemeToggle } from '@shared/theme/ThemeToggle';
import { routePath } from '@shared/routing/routeRegistry';

interface AccountMenuProps {
  displayName: string;
  avatarURL?: string;
  signOutLabel: string;
  working?: boolean;
  steward?: boolean;
  onSignOut: () => void;
  station?: 'user' | 'admin';
  /** Optional server-authoritative language control supplied by the station. */
  languageControl?: ReactNode;
}

export function AccountMenu({
  displayName,
  avatarURL,
  signOutLabel,
  working = false,
  steward = false,
  onSignOut,
  station = 'user',
  languageControl,
}: AccountMenuProps) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const menuId = useId();

  const accountHref =
    station === 'admin' ? routePath('admin', 'admin-settings') : routePath('user', 'account');
  return (
    <div className="nb-account-wrap">
      <button
        type="button"
        ref={triggerRef}
        className="nb-account-trigger"
        aria-expanded={open}
        aria-haspopup="dialog"
        aria-controls={menuId}
        onClick={() => setOpen((current) => !current)}
      >
        {avatarURL ? (
          <img
            className="nb-account-trigger__avatar"
            src={avatarURL}
            alt=""
            referrerPolicy="no-referrer"
          />
        ) : (
          <Icon name="account" />
        )}
        <span className="nb-account-trigger__name">{displayName}</span>
        <Icon name="chevron-down" />
      </button>
      <AnchoredSurface
        open={open}
        onClose={() => setOpen(false)}
        triggerRef={triggerRef}
        className="nb-account-popover"
      >
        <Dialog id={menuId} className="nb-account-menu" aria-label={t('shell.accountMenu')}>
          <FirstActionFocus />
          <div className="nb-account-menu__identity">{displayName}</div>
          <div className="nb-account-menu__actions">
            {languageControl}
            <ThemeToggle />
            <Link
              className="nb-btn nb-btn--ghost nb-btn--sm"
              to={accountHref}
              onClick={() => {
                setOpen(false);
                triggerRef.current?.focus();
              }}
            >
              <Icon name="account" />
              {station === 'admin' ? t('admin.settings.nav') : t('user.account.nav')}
            </Link>
            {station === 'user' && steward ? (
              <Link
                className="nb-btn nb-btn--ghost nb-btn--sm"
                to={routePath('user', 'steward')}
                onClick={() => {
                  setOpen(false);
                  triggerRef.current?.focus();
                }}
              >
                <Icon name="steward" />
                {t('user.steward.nav')}
              </Link>
            ) : null}
            <Button type="button" size="sm" onClick={onSignOut} disabled={working}>
              <Icon name="logout" />
              {working ? t('common.working') : signOutLabel}
            </Button>
          </div>
        </Dialog>
      </AnchoredSurface>
    </div>
  );
}
