import { NativeDialog } from '@shared/components/NativeDialog';
import { useEffect, useState } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import { Button } from '@shared/components/ui/Button';
import { Icon } from '@shared/components/Icon';
import { useOptionalAppearance } from './context';

export function BackgroundViewButton() {
  const { t } = useTranslation();
  const appearance = useOptionalAppearance();
  const [open, setOpen] = useState(false);
  const available = !!appearance && appearance.state.selected !== 'default';
  const active = open && available;
  useEffect(() => {
    if (!active) return;
    const overflow = document.body.style.overflow;
    document.documentElement.dataset.backgroundView = 'true';
    document.body.style.overflow = 'hidden';
    return () => {
      delete document.documentElement.dataset.backgroundView;
      document.body.style.overflow = overflow;
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
          <NativeDialog
            onDismiss={() => setOpen(false)}
            className="nb-background-view"
            aria-label={t('user.appearance.viewBackground')}
          >
            <Button autoFocus variant="secondary" onClick={() => setOpen(false)}>
              <Icon name="arrow-left" />
              {t('user.appearance.restorePage')}
            </Button>
          </NativeDialog>,
          document.body,
        )}
    </>
  );
}
