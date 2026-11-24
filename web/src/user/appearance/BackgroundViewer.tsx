import { NativeDialog } from '@shared/components/NativeDialog';
import { useLayoutEffect, useState, useSyncExternalStore } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import { Button } from '@shared/components/ui/Button';
import { Icon } from '@shared/components/Icon';
import { useBackgroundPlayback } from './playback-context';
import { useArtworkPolicy } from '@shared/artwork/context';
import { momentByKey } from './moments';
import { isRare } from '../activities/gacha/catalog';
import { useOptionalAppearance } from './context';

export function BackgroundViewButton({
  requestedScene,
  onReturn,
}: { requestedScene?: string; onReturn?: () => void } = {}) {
  const { t } = useTranslation();
  const appearance = useOptionalAppearance();
  const [openedScene, setOpenedScene] = useState<string>();
  const { playback, animated, retry } = useBackgroundPlayback();
  const policy = useArtworkPolicy();
  const id = appearance?.state.selected ?? 'default';
  const moment = momentByKey.get(appearance?.state.moment ?? '');
  const moving =
    id !== 'heart-moment' ||
    (!!moment?.card && isRare(moment.card.rarity) && policy.allowed(policy.momentKeys(moment.key)));
  const available =
    id !== 'default' &&
    appearance?.state.unlocked.includes(id) &&
    policy.allowed('appearance:' + id);
  const status = useSyncExternalStore(playback.subscribe, playback.getStatus);
  const scene = id + '/' + (appearance?.state.moment ?? '');
  const active = (onReturn ? requestedScene : openedScene) === scene && available;
  const close = () => {
    setOpenedScene(undefined);
    onReturn?.();
  };
  useLayoutEffect(() => {
    if (!active) return;
    const overflow = document.body.style.overflow;
    const position = { left: window.scrollX, top: window.scrollY };
    document.documentElement.dataset.backgroundView = 'true';
    document.body.style.overflow = 'hidden';
    return () => {
      delete document.documentElement.dataset.backgroundView;
      document.body.style.overflow = overflow;
      window.scrollTo({ ...position, behavior: 'instant' });
    };
  }, [active]);
  if (!available) return null;
  return (
    <>
      {!onReturn && (
        <Button
          variant="ghost"
          className="nb-background-view-trigger"
          aria-label={t('user.appearance.viewBackground')}
          title={t('user.appearance.viewBackground')}
          onClick={() => setOpenedScene(id + '/' + (appearance?.state.moment ?? ''))}
        >
          <Icon name="image" />
          <span>{t('user.appearance.viewBackground')}</span>
        </Button>
      )}
      {active &&
        createPortal(
          <NativeDialog
            onDismiss={close}
            className="nb-background-view"
            aria-label={t('user.appearance.viewBackground')}
          >
            <div className="nb-background-view__actions">
              {animated &&
                moving &&
                (status === 'failed' ? (
                  <>
                    <span role="status">{t('user.appearance.staticFallback')}</span>
                    <Button onClick={retry}>{t('user.appearance.retryMotion')}</Button>
                  </>
                ) : (
                  <Button onClick={() => playback.setPaused(status !== 'paused')}>
                    <Icon name={status === 'paused' ? 'play' : 'pause'} />
                    {t(status === 'paused' ? 'user.appearance.resume' : 'user.appearance.pause')}
                  </Button>
                ))}
              <Button autoFocus variant="secondary" onClick={close}>
                <Icon name="arrow-left" />
                {t('user.appearance.restorePage')}
              </Button>
            </div>
          </NativeDialog>,
          document.body,
        )}
    </>
  );
}
