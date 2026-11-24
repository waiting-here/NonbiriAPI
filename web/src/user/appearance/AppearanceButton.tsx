import { lazy, Suspense, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@shared/components/ui/Button';
import { Icon } from '@shared/components/Icon';
import { useOptionalAppearance } from './context';

const Options = lazy(() =>
  import('./AppearanceOptions').then((module) => ({ default: module.AppearanceOptions })),
);

export function AppearanceButton() {
  const { t } = useTranslation();
  const appearance = useOptionalAppearance();
  const [requested, setRequested] = useState(false);
  const [open, setOpen] = useState(false);
  if (!appearance) return null;
  return (
    <div className="nb-appearance-control">
      <Button
        variant="ghost"
        title={t('user.appearance.title')}
        aria-expanded={open}
        onClick={() => {
          setRequested(true);
          setOpen(true);
        }}
      >
        <Icon name="palette" />
        <span className="nb-appearance-control__label">{t('user.appearance.title')}</span>
      </Button>
      {requested && (
        <Suspense fallback={<span role="status">{t('common.loading')}</span>}>
          <Options open={open} setOpen={setOpen} />
        </Suspense>
      )}
    </div>
  );
}
