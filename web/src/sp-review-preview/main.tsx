import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { ArtworkPolicyProvider } from '@shared/artwork/policy';
import { ThemeProvider } from '@shared/theme/ThemeProvider';
import i18n from '@shared/i18n';
import '../user/i18n';
import '@shared/styles/index.css';
import '../user/activities/gacha/gacha.css';
import '../user/appearance/appearance.css';
import { AppearanceScene } from './AppearanceScene';
import { Preview } from './Preview';
import { entryById } from './fixture';
import './preview.css';

const query = new URLSearchParams(location.search);
const language = query.get('lang');
if (language === 'zh' || language === 'en') void i18n.changeLanguage(language);
const requestedScene = entryById[query.get('scene') ?? ''];
const scene =
  requestedScene?.kind === 'tomorrow' && requestedScene.refined ? undefined : requestedScene;
if (scene)
  document.documentElement.dataset.theme = query.get('theme') === 'dark' ? 'dark' : 'light';
document.documentElement.lang = query.get('lang') === 'en' ? 'en' : 'zh-CN';
createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ArtworkPolicyProvider disabled>
      {scene ? (
        <AppearanceScene
          entry={scene}
          dark={query.get('theme') === 'dark'}
          animated={query.get('motion') !== '0'}
          english={query.get('lang') === 'en'}
        />
      ) : (
        <ThemeProvider>
          <Preview />
        </ThemeProvider>
      )}
    </ArtworkPolicyProvider>
  </StrictMode>,
);
