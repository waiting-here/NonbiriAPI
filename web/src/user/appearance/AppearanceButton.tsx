import { useState } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import { ConfirmDialog } from '@shared/components/ConfirmDialog';
import { Button } from '@shared/components/ui/Button';
import { Icon } from '@shared/components/Icon';
import { ErrorState, LoadingState } from '@shared/components/States';
import { ThemeToggle } from '@shared/theme/ThemeToggle';
import { appearances, useOptionalAppearance } from './context';

export function AppearanceButton() {
  const { t, i18n } = useTranslation();
  const appearance = useOptionalAppearance();
  const [open, setOpen] = useState(false);
  const [choice, setChoice] = useState('default');
  const [error, setError] = useState<unknown>(null);
  if (!appearance) return null;
  const english = i18n.language.startsWith('en');
  const available = appearance.state.unlocked.includes(choice);
  return (
    <div className="nb-appearance-control">
      <Button
        variant="ghost"
        onClick={() => {
          setChoice(appearance.state.selected);
          setError(null);
          setOpen(true);
        }}
      >
        <Icon name="spark" />
        {t('user.appearance.title')}
      </Button>
      {createPortal(
        <ConfirmDialog
          open={open}
          title={t('user.appearance.title')}
          description={t('user.appearance.description')}
          confirmLabel={t('user.appearance.apply')}
          cancelLabel={t('common.close')}
          confirmDisabled={!available || appearance.pending || !!appearance.error}
          busy={appearance.saving}
          onCancel={() => setOpen(false)}
          onConfirm={() => {
            setError(null);
            void appearance.select(choice).then(() => setOpen(false), setError);
          }}
        >
          <ThemeToggle />
          {appearance.pending ? <LoadingState /> : null}
          {appearance.error ? (
            <ErrorState error={appearance.error} onRetry={appearance.refresh} />
          ) : null}
          {error ? <ErrorState error={error} /> : null}
          <div
            className="nb-appearance-options"
            role="radiogroup"
            aria-label={t('user.appearance.title')}
          >
            {appearances.map((item) => {
              const unlocked = appearance.state.unlocked.includes(item.id);
              return (
                <label
                  key={item.id}
                  className="nb-appearance-option"
                  data-selected={choice === item.id}
                  data-locked={!unlocked}
                >
                  <input
                    type="radio"
                    name="appearance"
                    value={item.id}
                    checked={choice === item.id}
                    disabled={!unlocked || appearance.saving}
                    onChange={() => setChoice(item.id)}
                  />
                  <span className="nb-appearance-option__image" data-design={item.id}>
                    {'light' in item && (
                      <>
                        <img
                          className="nb-appearance-light"
                          src={item.light}
                          alt=""
                          loading="lazy"
                        />
                        <img className="nb-appearance-dark" src={item.dark} alt="" loading="lazy" />
                      </>
                    )}
                    {item.id === 'default' && <Icon name="spark" />}
                  </span>
                  <span className="nb-appearance-option__copy">
                    <strong>{english ? item.name_en : item.name}</strong>
                    <span>{english ? item.description_en : item.description}</span>
                    <small>
                      {unlocked
                        ? t(
                            choice === item.id
                              ? 'user.appearance.chosen'
                              : 'user.appearance.available',
                          )
                        : t('user.appearance.locked')}
                    </small>
                  </span>
                </label>
              );
            })}
          </div>
        </ConfirmDialog>,
        document.body,
      )}
    </div>
  );
}
