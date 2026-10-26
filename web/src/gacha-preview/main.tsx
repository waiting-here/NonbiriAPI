import { StrictMode, useEffect, useState } from 'react';
import { ArtworkPolicyProvider } from '@shared/artwork/policy';
import { createRoot } from 'react-dom/client';
import { BrowserRouter } from 'react-router';
import { QueryClient, QueryClientProvider, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import '../user/i18n';
import '@shared/styles/index.css';
import { ThemeProvider } from '@shared/theme/ThemeProvider';
import { ThemeToggle } from '@shared/theme/ThemeToggle';
import { GachaApp } from '../user/activities/gacha/GachaApp';
import { DrawReveal } from '../user/activities/gacha/DrawReveal';
import { GachaAudio } from '../user/activities/gacha/audio';
import type { DrawReceipt } from '../user/activities/gacha/types';
import { AppearanceProvider } from '../user/appearance/AppearanceProvider';
import { AppearanceButton } from '../user/appearance/AppearanceButton';
import { appearanceKey } from '../user/appearance/context';
import { localPort, localAppearancePort, appearancePreviewPort, resetPreview } from './local-port';
import './preview.css';
import { AppearancePreview } from './AppearancePreview';
import { AmberPreview } from './AmberPreview';
import { AmberExport } from './AmberExport';
import { SeasonalPreview } from './SeasonalPreview';
import { sampleReceipt, previewPreferences, type Scenario } from './scenarios';

const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
function Preview() {
  const [revision, setRevision] = useState(0);
  const [backgroundOnly, setBackgroundOnly] = useState(false);
  const [appearanceView, setAppearanceView] = useState(
    new URLSearchParams(location.search).get('view') === 'appearance',
  );
  const [english, setEnglish] = useState(new URLSearchParams(location.search).get('lang') === 'en');
  const [demo, setDemo] = useState<DrawReceipt | null>(() => {
    const run = new URLSearchParams(location.search).get('run');
    return run && ['ten', 'plain', 'ssr', 'sp2', 'n', 'seasonal'].includes(run)
      ? sampleReceipt(run as Scenario)
      : null;
  });
  const [demoPreferences, setDemoPreferences] = useState(previewPreferences);
  const [audio] = useState(() => new GachaAudio());
  useEffect(() => () => audio.dispose(), [audio]);
  const query = useQueryClient();
  const { i18n } = useTranslation();
  const reset = (mode: 'new' | 'collection') => {
    resetPreview(mode);
    void query.invalidateQueries({ queryKey: appearanceKey('preview') });
    setRevision((v) => v + 1);
  };
  const play = (scenario: Scenario) => {
    setDemoPreferences(previewPreferences());
    setDemo(sampleReceipt(scenario));
  };
  return (
    <BrowserRouter>
      <AppearanceProvider port={appearanceView ? appearancePreviewPort : localAppearancePort}>
        <div className="gacha-preview">
          <aside className="gacha-preview-controls" aria-label="预览控制">
            <span>
              {english ? 'LOCAL PREVIEW · independent sample data' : '本地预览 · 使用独立体验数据'}
            </span>
            <div>
              <button
                onClick={() => {
                  const next = !appearanceView;
                  setAppearanceView(next);
                  const url = new URL(location.href);
                  if (next) url.searchParams.set('view', 'appearance');
                  else url.searchParams.delete('view');
                  history.replaceState(null, '', url);
                }}
              >
                {appearanceView
                  ? english
                    ? 'Back to draws'
                    : '返回抽卡预览'
                  : english
                    ? 'Appearance preview'
                    : '装扮可读性预览'}
              </button>
              {appearanceView && (
                <button
                  className="nb-btn nb-btn--secondary"
                  onClick={() => setBackgroundOnly((v) => !v)}
                >
                  {backgroundOnly
                    ? english
                      ? 'Show components'
                      : '显示页面组件'
                    : english
                      ? 'View background'
                      : '欣赏背景'}
                </button>
              )}
              {!appearanceView && (
                <>
                  <button onClick={() => reset('new')}>
                    {english ? 'New account' : '新账号体验'}
                  </button>
                  <button onClick={() => reset('collection')}>
                    {english ? 'Full collection' : '完整收藏体验'}
                  </button>
                  <button onClick={() => play('ten')}>
                    {english ? 'SSR + SP ten-pull' : 'SSR＋SP 十连'}
                  </button>
                  <button onClick={() => play('plain')}>
                    {english ? 'SR ten-pull' : 'SR 十连'}
                  </button>
                  <button onClick={() => play('n')}>{english ? 'N single' : 'N 单抽'}</button>
                  <button onClick={() => play('sp2')}>{english ? 'SP reunion' : 'SP 重逢'}</button>
                </>
              )}
              <AppearanceButton />
              <ThemeToggle />
              <button
                onClick={() => {
                  setEnglish(!english);
                  void i18n.changeLanguage(english ? 'zh' : 'en');
                }}
              >
                {english ? '中文' : 'English'}
              </button>
            </div>
          </aside>
          <main hidden={appearanceView && backgroundOnly}>
            {appearanceView ? (
              <AppearancePreview english={english} />
            ) : (
              <GachaApp key={revision} port={localPort} english={english} />
            )}
          </main>
          {demo && (
            <DrawReveal
              receipt={demo}
              key={demo.id}
              preferences={demoPreferences}
              audio={audio}
              english={english}
              onClose={() => {
                setDemo(null);
                audio.stopEffects();
              }}
            />
          )}
        </div>
      </AppearanceProvider>
    </BrowserRouter>
  );
}
const amberPreview = new URLSearchParams(location.search).get('view') === 'amber';
const amberExport = new URLSearchParams(location.search).get('view') === 'amber-export';
function PreviewRoot() {
  const [adult, setAdult] = useState(() => new URLSearchParams(location.search).get('r18') === '1');
  const seasonal = new URLSearchParams(location.search).get('view') === 'seasonal';
  return (
    <ArtworkPolicyProvider disabled={!adult}>
      <ThemeProvider>
        {seasonal ? (
          <SeasonalPreview adult={adult} setAdult={setAdult} />
        ) : amberExport ? (
          <AmberExport />
        ) : amberPreview ? (
          <AmberPreview />
        ) : (
          <Preview />
        )}
      </ThemeProvider>
    </ArtworkPolicyProvider>
  );
}
createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={client}>
      <PreviewRoot />
    </QueryClientProvider>
  </StrictMode>,
);
