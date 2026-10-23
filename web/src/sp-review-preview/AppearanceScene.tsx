import { useEffect, useRef, useState } from 'react';
import { Button } from '@shared/components/ui/Button';
import { Field } from '@shared/components/ui/Field';
import { Panel, PanelBody, PanelHead } from '@shared/components/ui/Panel';
import { artSourceSet } from '../user/activities/gacha/art-resolution';
import { isSpecialTheme } from '../user/activities/gacha/special-scenes';
import { TomorrowArtwork } from '../user/activities/gacha/tomorrow/Artwork';
import { AmberStill } from '../user/appearance/AmberStill';
import { MirrorSeaStill } from '../user/appearance/MirrorSeaStill';
import { RomanceStill } from '../user/appearance/RomanceStill';
import { previewControlMessage, previewReadyMessage, useReducedMotion } from './controls';
import { appearanceArt, entryName, type Entry } from './fixture';

function PublishedBackdrop({
  entry,
  dark,
  animated,
  paused,
}: {
  entry: Extract<Entry, { kind: 'published' }>;
  dark: boolean;
  animated: boolean;
  paused: boolean;
}) {
  const canvas = useRef<HTMLCanvasElement>(null);
  const pausedRef = useRef(paused);
  const [high, setHigh] = useState(
    () => Math.max(innerWidth, innerHeight) * (devicePixelRatio || 1) > 1920,
  );
  useEffect(() => {
    pausedRef.current = paused;
  }, [paused]);
  useEffect(() => {
    const update = () =>
      setHigh(Math.max(innerWidth, innerHeight) * (devicePixelRatio || 1) > 1920);
    window.addEventListener('resize', update);
    return () => window.removeEventListener('resize', update);
  }, []);
  const appearance = entry.appearance;
  const legacyPaused = !isSpecialTheme(appearance) && paused;
  useEffect(() => {
    const surface = canvas.current;
    if (!surface) return;
    surface.dataset.ready = 'false';
    if (!animated || legacyPaused) return;
    const controller = new AbortController();
    let release: (() => void) | undefined;
    const rendering = isSpecialTheme(appearance)
      ? import('../user/activities/gacha/special-renderer').then(({ renderSpecial }) =>
          renderSpecial(surface, appearance, {
            landscape: true,
            variant: 'appearance',
            dark,
            highResolution: high,
            paused: () => pausedRef.current,
            signal: controller.signal,
            ambient: true,
          }),
        )
      : import('../user/appearance/wallpaper-motion').then(({ renderWallpaper }) =>
          renderWallpaper(
            surface,
            appearance,
            dark ? 'dark' : 'light',
            controller.signal,
            high ? 'high' : 'standard',
          ),
        );
    void rendering
      .then((stop) => {
        if (controller.signal.aborted) stop();
        else release = stop;
      })
      .catch(() => {
        /* The matching static composition remains visible. */
      });
    return () => {
      controller.abort();
      release?.();
      surface.dataset.ready = 'false';
    };
  }, [appearance, animated, dark, high, legacyPaused]);
  const src = appearanceArt(entry, dark);
  return (
    <>
      {appearance === 'amber' ? (
        <AmberStill dark={dark} />
      ) : appearance === 'mirrorsea' ? (
        <MirrorSeaStill dark={dark} />
      ) : appearance === 'elopement' || appearance === 'heart-thief' ? (
        <RomanceStill theme={appearance} dark={dark} />
      ) : (
        <img
          className="nb-appearance-still"
          src={src}
          srcSet={artSourceSet(src)}
          sizes="(max-aspect-ratio: 16/9) 177.78vh, 100vw"
          alt=""
          aria-hidden="true"
        />
      )}
      {animated && <canvas ref={canvas} className="nb-appearance-backdrop" data-ready="false" />}
    </>
  );
}

export function AppearanceScene({
  entry,
  dark: initialDark,
  animated: initialAnimated,
  english,
}: {
  entry: Entry;
  dark: boolean;
  animated: boolean;
  english: boolean;
}) {
  const [dark, setDark] = useState(initialDark);
  const [motion, setMotion] = useState(initialAnimated);
  const [controls, setControls] = useState(false);
  const [localPaused, setLocalPaused] = useState(false);
  const [parentPaused, setParentPaused] = useState(false);
  const reduced = useReducedMotion();
  const animated = motion && !reduced;
  const paused = localPaused || parentPaused;
  const embedded = window.parent !== window;
  const t = (zh: string, en: string) => (english ? en : zh);
  const legacyPaused =
    entry.kind === 'published' && !isSpecialTheme(entry.appearance) && animated && paused;
  useEffect(() => {
    document.documentElement.dataset.theme = dark ? 'dark' : 'light';
    document.documentElement.dataset.appearance =
      entry.kind === 'published' ? entry.appearance : 'tomorrow';
    return () => {
      delete document.documentElement.dataset.appearance;
    };
  }, [dark, entry]);
  useEffect(() => {
    const receive = (event: MessageEvent) => {
      if (event.origin !== location.origin || event.source !== window.parent) return;
      if (!event.data || typeof event.data !== 'object') return;
      const message = event.data as { type?: unknown; paused?: unknown };
      if (message.type === previewControlMessage && typeof message.paused === 'boolean')
        setParentPaused(message.paused);
    };
    window.addEventListener('message', receive);
    if (window.parent !== window)
      window.parent.postMessage({ type: previewReadyMessage }, location.origin);
    return () => window.removeEventListener('message', receive);
  }, []);
  if (entry.kind === 'tomorrow' && entry.poseStudy)
    return (
      <main className="sp-review__main">
        <h1>{entryName(entry, english)}</h1>
        <p>
          {t(
            '新姿态目前只有静态竖卡，尚未制作主题装扮。',
            'The new pose is a still portrait; its appearance has not been made yet.',
          )}
        </p>
        <a className="nb-btn nb-btn--secondary" href="/">
          {t('返回对照页', 'Back to comparison')}
        </a>
      </main>
    );
  return (
    <div
      className={
        'sp-scene sp-scene--' + (entry.kind === 'published' ? entry.appearance : 'tomorrow')
      }
    >
      <h1 className="sr-only">
        {entryName(entry, english) + t('装扮预览', ' appearance preview')}
      </h1>
      <div className="sp-scene__art" aria-hidden="true">
        {entry.kind === 'tomorrow' ? (
          <TomorrowArtwork
            variant="appearance"
            dark={dark}
            animated={animated}
            paused={paused}
            className="sp-scene__tomorrow"
          />
        ) : (
          <PublishedBackdrop entry={entry} dark={dark} animated={animated} paused={paused} />
        )}
      </div>
      <div className="sp-scene__tools">
        <Button size="sm" aria-pressed={controls} onClick={() => setControls(!controls)}>
          {controls
            ? t('隐藏页面组件', 'Hide page controls')
            : t('显示页面组件', 'Show page controls')}
        </Button>
        {!embedded && (
          <>
            <Button size="sm" onClick={() => setDark(!dark)}>
              {dark ? t('切换浅色', 'Light appearance') : t('切换深色', 'Dark appearance')}
            </Button>
            <Button size="sm" disabled={reduced} onClick={() => setMotion(!motion)}>
              {animated ? t('静态回退', 'Static fallback') : t('播放动作', 'Play motion')}
            </Button>
            {animated && (
              <Button
                size="sm"
                aria-pressed={localPaused}
                onClick={() => setLocalPaused(!localPaused)}
              >
                {localPaused ? t('继续动作', 'Resume motion') : t('暂停动作', 'Pause motion')}
              </Button>
            )}
            <a className="nb-btn nb-btn--secondary nb-btn--sm" href="/">
              {t('返回对照页', 'Back to comparison')}
            </a>
          </>
        )}
        {reduced && (
          <span className="sp-scene__status">
            {t('已跟随系统减弱动效', 'Following reduced motion')}
          </span>
        )}
        {parentPaused && !legacyPaused && (
          <span className="sp-scene__status">{t('预览已暂停', 'Preview paused')}</span>
        )}
        {legacyPaused && (
          <span className="sp-scene__status">
            {t(
              '此对照装扮暂停时显示静态回退',
              'This comparison uses its static fallback while paused',
            )}
          </span>
        )}
      </div>
      {controls && (
        <main className="sp-scene__content">
          <Panel>
            <PanelHead
              title={t('欢迎回来', 'Welcome back')}
              description={t(
                '仅检查文字、输入框与背景的搭配。',
                'Preview text and controls against the artwork.',
              )}
            />
            <PanelBody>
              <form onSubmit={(event) => event.preventDefault()}>
                <Field label={t('端点名称', 'Endpoint name')}>
                  {(props) => (
                    <input {...props} defaultValue={t('我的 API 端点', 'My API endpoint')} />
                  )}
                </Field>
                <Field label={t('模型', 'Model')}>
                  {(props) => (
                    <select {...props}>
                      <option>ChatGPT</option>
                      <option>Claude</option>
                    </select>
                  )}
                </Field>
                <div className="sp-review__actions">
                  <Button type="button" variant="primary">
                    {t('主要按钮', 'Primary button')}
                  </Button>
                  <Button type="button">{t('次要按钮', 'Secondary button')}</Button>
                </div>
              </form>
            </PanelBody>
          </Panel>
        </main>
      )}
    </div>
  );
}
