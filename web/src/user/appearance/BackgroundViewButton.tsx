import { useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import { Button } from '@shared/components/ui/Button';
import { Icon } from '@shared/components/Icon';
import { useOptionalAppearance } from './context';

export function BackgroundViewButton() {
  const { t } = useTranslation();
  const appearance = useOptionalAppearance();
  const [open, setOpen] = useState(false);
  const dialog = useRef<HTMLDialogElement>(null);
  const available = !!appearance && appearance.state.selected !== 'default';
  const active = open && available;
  useEffect(() => {
    if (!active) return;
    const element = dialog.current;
    const previous = document.activeElement;
    const overflow = document.body.style.overflow;
    document.documentElement.dataset.backgroundView = 'true';
    document.body.style.overflow = 'hidden';
    element?.showModal();
    return () => {
      element?.close();
      delete document.documentElement.dataset.backgroundView;
      document.body.style.overflow = overflow;
      if (previous instanceof HTMLElement && previous.isConnected) previous.focus();
    };
  }, [active]);
  if (!available) return null;
  return (
    <>
      <Button
        variant="ghost"
        className="nb-background-view-trigger"
        aria-label={t('user.appearance.viewBackground')}
        title={t('user.appearance.viewBackground')}
        onClick={() => setOpen(true)}
      >
        <Icon name="spark" />
        <span>{t('user.appearance.viewBackground')}</span>
      </Button>
      {active &&
        createPortal(
          <dialog
            ref={dialog}
            className="nb-background-view"
            aria-label={t('user.appearance.viewBackground')}
            onCancel={(event) => {
              event.preventDefault();
              setOpen(false);
            }}
          >
            <Button autoFocus variant="secondary" onClick={() => setOpen(false)}>
              <Icon name="arrow-left" />
              {t('user.appearance.restorePage')}
            </Button>
          </dialog>,
          document.body,
        )}
    </>
  );
}
