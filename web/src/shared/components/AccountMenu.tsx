import { useEffect, useId, useRef, useState, type ReactNode } from 'react';
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
  const wrapRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const menuId = useId();

  useEffect(() => {
    if (!open) return;
    const onPointerDown = (event: PointerEvent) => {
      if (wrapRef.current && !wrapRef.current.contains(event.target as Node)) setOpen(false);
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        setOpen(false);
        triggerRef.current?.focus();
        return;
      }
      if (event.key !== 'Tab') return;
      const focusable = Array.from(menuRef.current?.querySelectorAll<HTMLElement>(
        'a[href], button:not([disabled]), select:not([disabled])',
      ) ?? []);
      if (focusable.length === 0) return;
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    document.addEventListener('pointerdown', onPointerDown);
    document.addEventListener('keydown', onKeyDown);
    const first = menuRef.current?.querySelector<HTMLElement>('a[href], button:not([disabled]), select:not([disabled])');
    first?.focus();
    return () => {
      document.removeEventListener('pointerdown', onPointerDown);
      document.removeEventListener('keydown', onKeyDown);
    };
  }, [open]);

  const accountHref = station === 'admin' ? routePath('admin', 'admin-settings') : routePath('user', 'account');
  return (
    <div className="nb-account-wrap" ref={wrapRef}>
      <button
        type="button"
        ref={triggerRef}
        className="nb-account-trigger"
        aria-expanded={open}
        aria-haspopup="dialog"
        aria-controls={menuId}
        onClick={() => setOpen((current) => !current)}
      >
        {avatarURL ? <img className="nb-account-trigger__avatar" src={avatarURL} alt="" referrerPolicy="no-referrer" /> : <Icon name="account" />}
        <span className="nb-account-trigger__name">{displayName}</span>
        <Icon name="chevron-down" />
      </button>
      {open ? (
        <div id={menuId} ref={menuRef} className="nb-account-menu" role="dialog" aria-label={t('shell.accountMenu')}>
          <div className="nb-account-menu__identity">{displayName}</div>
          <div className="nb-account-menu__actions">
            {languageControl}
            <ThemeToggle />
            <Link className="nb-button nb-button--ghost nb-button--small" to={accountHref} onClick={() => { setOpen(false); triggerRef.current?.focus(); }}>
              <Icon name="account" />
              {station === 'admin' ? t('admin.settings.nav') : t('user.account.nav')}
            </Link>
            {station === 'user' && steward ? (
              <Link className="nb-button nb-button--ghost nb-button--small" to={routePath('user', 'steward')} onClick={() => { setOpen(false); triggerRef.current?.focus(); }}>
                <Icon name="steward" />
                {t('user.steward.nav')}
              </Link>
            ) : null}
            <button type="button" className="nb-button nb-button--secondary nb-button--small" onClick={onSignOut} disabled={working}>
              <Icon name="logout" />
              {working ? t('common.working') : signOutLabel}
            </button>
          </div>
        </div>
      ) : null}
    </div>
  );
}
