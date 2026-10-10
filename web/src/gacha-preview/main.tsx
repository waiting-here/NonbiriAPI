import { StrictMode, useState } from 'react';
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

const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
function Preview() {
  const [revision, setRevision] = useState(0);
  const [english, setEnglish] = useState(false);
  const [demo, setDemo] = useState<DrawReceipt | null>(null);
  const [audio] = useState(() => new GachaAudio());
  const query = useQueryClient();
  const { i18n } = useTranslation();
  const reset = (mode: 'new' | 'collection') => {
    resetPreview(mode);
    void query.invalidateQueries({ queryKey: appearanceKey('preview') });
    setRevision((v) => v + 1);
  };
  const play = (rarity: 'SSR' | 'SP') => {
    audio.cue('gather', true);
    const ids = [
      'a-chatgpt',
      'b-glm',
      'd-claude',
      rarity === 'SSR' ? 'i-chatgpt' : 'l-chatgpt',
      'a-deepseek',
      'e-gemini',
      'c-chatgpt',
      'f-glm',
      rarity === 'SSR' ? 'j-deepseek' : 'm-deepseek',
      'b-gemini',
    ];
    setDemo({
      id: 'preview-' + crypto.randomUUID(),
      pool_id: 'preview',
      pool_name: english ? 'Presentation sample' : '演出样片',
      created_at: Math.floor(Date.now() / 1000),
      items: ids.map((card_id) => ({ card_id, duplicate: true, guarantee: 'none' })),
    });
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
              <button onClick={() => play('SSR')}>{english ? 'SSR animation' : 'SSR 演出'}</button>
              <button onClick={() => play('SP')}>{english ? 'SP animation' : 'SP 演出'}</button>
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
              preferences={{
                music: false,
                sound: true,
                vibration: true,
                reduced: matchMedia('(prefers-reduced-motion: reduce)').matches,
              }}
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
