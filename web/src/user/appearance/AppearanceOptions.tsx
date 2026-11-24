import { useEffect, useId, useRef, useState, useSyncExternalStore } from 'react';
import { useTranslation } from 'react-i18next';
import { Sheet } from '@shared/components/ui/Sheet';
import { BackgroundViewButton } from './BackgroundViewButton';
import { useBackgroundPlayback } from './playback-context';
import { MomentPicker } from './MomentPicker';
import { cardById } from '../activities/gacha/catalog';
import { Button } from '@shared/components/ui/Button';
import { Segmented } from '@shared/components/ui/Segmented';
import { Icon } from '@shared/components/Icon';
import { ErrorState, LoadingState } from '@shared/components/States';
import { ThemeToggle } from '@shared/theme/ThemeToggle';
import { useTheme } from '@shared/theme/useTheme';
import type { AppearanceIntensity } from '@shared/theme/context';
import { defaultMoment, momentByKey, momentArtworkKeys } from './moments';
import { ArtworkImage, ArtworkBadge, ArtworkUnavailable } from '@shared/artwork/ArtworkImage';
import { useArtworkPolicy } from '@shared/artwork/context';
import { appearances, useOptionalAppearance } from './context';

export function AppearanceOptions({
  open,
  setOpen,
}: {
  open: boolean;
  setOpen: (next: boolean) => void;
}) {
  const { t, i18n } = useTranslation();
  const appearance = useOptionalAppearance();
  const policy = useArtworkPolicy();
  const [requestedScene, setRequestedScene] = useState<string>();
  const returnFocus = useRef(false);
  const viewButton = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (open && returnFocus.current) {
      returnFocus.current = false;
      viewButton.current?.focus({ preventScroll: true });
    }
  }, [open]);
  const [choice, setChoice] = useState('default');
  const [error, setError] = useState<unknown>(null);
  const { intensity, setIntensity } = useTheme();
  const { playback, motion, quality, setMotion, setQuality, animated, retry } =
    useBackgroundPlayback();
  const status = useSyncExternalStore(playback.subscribe, playback.getStatus);
  const hint = useId();
  const previousOpen = useRef(false);
  useEffect(() => {
    if (open && !previousOpen.current) {
      setChoice(appearance?.state.selected ?? 'default');
      setError(null);
    }
    previousOpen.current = open;
  }, [open, appearance?.state.selected]);
  if (!appearance) return null;
  const english = i18n.language.startsWith('en');
  const moment = momentByKey.get(appearance.state.moment ?? '') ?? defaultMoment;
  const keys = (id: string) =>
    id === 'heart-moment'
      ? ['appearance:' + id, ...momentArtworkKeys(moment)]
      : ['appearance:' + id];
  const available = appearance.state.unlocked.includes(choice) && policy.allowed(keys(choice));
  return (
    <div className="nb-appearance-control">
      <Sheet
        open={open}
        title={t('user.appearance.title')}
        description={t('user.appearance.description')}
        closeLabel={t('common.close')}
        busy={appearance.saving}
        onClose={() => setOpen(false)}
        footer={
          choice !== 'heart-moment' && (
            <Button
              variant="primary"
              disabled={!available || appearance.pending || !!appearance.error || appearance.saving}
              onClick={() => {
                setError(null);
                void appearance.select(choice).then(() => setOpen(false), setError);
              }}
            >
              {t('user.appearance.apply')}
            </Button>
          )
        }
      >
        <ThemeToggle />
        {animated && status === 'failed' && (
          <div role="status">
            {t('user.appearance.staticFallback')}{' '}
            <Button onClick={retry}>{t('user.appearance.retryMotion')}</Button>
          </div>
        )}
        {appearance.state.selected !== 'default' &&
          policy.allowed('appearance:' + appearance.state.selected) && (
            <Button
              ref={viewButton}
              className="nb-background-view-in-sheet"
              onClick={() => {
                setRequestedScene(
                  appearance.state.selected + '/' + (appearance.state.moment ?? ''),
                );
                setOpen(false);
              }}
            >
              <Icon name="image" />
              {t('user.appearance.viewBackground')}
            </Button>
          )}
        <div className="nb-appearance-setting">
          <strong>{t('user.appearance.motion')}</strong>
          <Segmented
            label={t('user.appearance.motion')}
            value={motion}
            onChange={setMotion}
            options={[
              { value: 'system', label: t('user.appearance.system') },
              { value: 'full', label: t('user.appearance.full') },
              { value: 'static', label: t('user.appearance.static') },
            ]}
          />
        </div>
        <div className="nb-appearance-setting">
          <strong>{t('user.appearance.quality')}</strong>
          <Segmented
            label={t('user.appearance.quality')}
            value={quality}
            onChange={setQuality}
            options={[
              { value: 'auto', label: t('user.appearance.auto') },
              { value: 'highest', label: t('user.appearance.highest') },
            ]}
          />
        </div>
        <div className="nb-appearance-intensity">
          <span id={hint} className="nb-small nb-muted">
            {t('user.appearance.intensityHint')}
          </span>
          <Segmented<AppearanceIntensity>
            label={t('user.appearance.intensity')}
            describedBy={hint}
            value={intensity}
            onChange={setIntensity}
            options={[
              { value: 'vivid', label: t('user.appearance.intensityVivid') },
              { value: 'balanced', label: t('user.appearance.intensityBalanced') },
              { value: 'focus', label: t('user.appearance.intensityFocus') },
            ]}
          />
        </div>
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
          {appearances
            .filter(
              (item) =>
                appearance.state.unlocked.includes(item.id) ||
                appearance.state.visible?.includes(item.id),
            )
            .map((item) => {
              const unlocked = appearance.state.unlocked.includes(item.id);
              return (
                <label
                  key={item.id}
                  data-preview={item.id}
                  className="nb-appearance-option"
                  data-selected={choice === item.id}
                  data-locked={!unlocked}
                  data-unavailable={!policy.allowed(keys(item.id))}
                >
                  <input
                    type="radio"
                    name="appearance"
                    value={item.id}
                    checked={choice === item.id}
                    disabled={!unlocked || appearance.saving || !policy.allowed(keys(item.id))}
                    onChange={() => setChoice(item.id)}
                  />
                  <span className="nb-appearance-option__image" data-design={item.id}>
                    {'card' in item && item.card && (
                      <ArtworkImage
                        artworkKey={keys(item.id)}
                        src={cardById[item.card].thumb}
                        alt=""
                        loading="lazy"
                      />
                    )}
                    {item.id === 'heart-moment' && (
                      <ArtworkImage
                        artworkKey={keys(item.id)}
                        src={
                          (momentByKey.get(appearance.state.moment ?? '') ?? defaultMoment).thumb
                        }
                        alt=""
                        loading="lazy"
                      />
                    )}
                    {item.id === 'default' && <Icon name="spark" />}
                  </span>
                  <span className="nb-appearance-option__copy">
                    <strong>{english ? item.name_en : item.name}</strong>
                    <ArtworkBadge artworkKey={keys(item.id)} />
                    <ArtworkUnavailable artworkKey={keys(item.id)} />
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
        {choice === 'heart-moment' && <MomentPicker />}
      </Sheet>
      <BackgroundViewButton
        requestedScene={requestedScene}
        onReturn={() => {
          returnFocus.current = true;
          setRequestedScene(undefined);
          setOpen(true);
        }}
      />
    </div>
  );
}
