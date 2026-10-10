import { StrictMode, useEffect, useState } from 'react';
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
import { localPort, localAppearancePort, resetPreview } from './local-port';
import './preview.css';
import { sampleReceipt, previewPreferences, type Scenario } from './scenarios';

const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
function Preview() {
  const [revision, setRevision] = useState(0);
  const [english, setEnglish] = useState(new URLSearchParams(location.search).get('lang') === 'en');
  const [demo, setDemo] = useState<DrawReceipt | null>(() => {
    const run = new URLSearchParams(location.search).get('run');
    return run && ['ten', 'plain', 'ssr', 'sp2', 'n'].includes(run)
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
      <AppearanceProvider port={localAppearancePort}>
        <div className="gacha-preview">
          <aside className="gacha-preview-controls" aria-label="预览控制">
            <span>
              {english ? 'LOCAL PREVIEW · independent sample data' : '本地预览 · 使用独立体验数据'}
            </span>
            <div>
              <button onClick={() => reset('new')}>{english ? 'New account' : '新账号体验'}</button>
              <button onClick={() => reset('collection')}>
                {english ? 'Full collection' : '完整收藏体验'}
              </button>
              <button onClick={() => play('ten')}>
                {english ? 'SSR + SP ten-pull' : 'SSR＋SP 十连'}
              </button>
              <button onClick={() => play('plain')}>{english ? 'SR ten-pull' : 'SR 十连'}</button>
              <button onClick={() => play('n')}>{english ? 'N single' : 'N 单抽'}</button>
              <button onClick={() => play('sp2')}>{english ? 'SP reunion' : 'SP 重逢'}</button>
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
          <main>
            <GachaApp key={revision} port={localPort} english={english} />
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
createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={client}>
      <ThemeProvider>
        <Preview />
      </ThemeProvider>
    </QueryClientProvider>
  </StrictMode>,
);
