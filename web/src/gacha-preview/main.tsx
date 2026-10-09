import { StrictMode, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { BrowserRouter } from 'react-router';
import '../user/i18n';
import '@shared/styles/index.css';
import { GachaApp } from '../user/activities/gacha/GachaApp';
import { localPort, resetPreview } from './local-port';
import './preview.css';

function Preview() {
  const [revision, setRevision] = useState(0);
  const [english, setEnglish] = useState(false);
  const [dark, setDark] = useState(false);
  const reset = (mode: 'new' | 'collection') => {
    resetPreview(mode);
    setRevision((v) => v + 1);
  };
  return (
    <BrowserRouter>
      <div className="gacha-preview" data-theme={dark ? 'dark' : 'light'}>
        <aside className="gacha-preview-controls" aria-label="预览控制">
          <span>
            {english ? 'LOCAL PREVIEW · sample data only' : '本地预览 · 使用独立体验数据'}
          </span>
          <div>
            <button onClick={() => reset('new')}>{english ? 'New account' : '新账号体验'}</button>
            <button onClick={() => reset('collection')}>
              {english ? 'Full collection' : '完整收藏体验'}
            </button>
            <button onClick={() => setDark(!dark)}>{dark ? '浅色' : '深色'}</button>
            <button onClick={() => setEnglish(!english)}>{english ? '中文' : 'English'}</button>
          </div>
        </aside>
        <main>
          <GachaApp key={revision} port={localPort} english={english} />
        </main>
      </div>
    </BrowserRouter>
  );
}
createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <Preview />
  </StrictMode>,
);
