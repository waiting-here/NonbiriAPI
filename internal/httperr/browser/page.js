(() => {
  const root = document.documentElement;
  const stored = (key) => {
    try { return localStorage.getItem(key); } catch { return null; }
  };
  const save = (key, value) => {
    try { localStorage.setItem(key, value); } catch { /* Preferences remain usable without storage. */ }
  };
  const language = stored('nb.lang');
  if (language === 'zh' || language === 'en') root.lang = language;
  const theme = stored('nb.theme');
  root.dataset.theme = theme === 'dark' || theme === 'light'
    ? theme
    : (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light');
  document.addEventListener('DOMContentLoaded', () => {
    const languageButton = document.getElementById('browser-language');
    const themeButton = document.getElementById('browser-theme');
    const sync = () => {
      document.title = document.querySelector('#browser-title [lang="' + root.lang + '"]').textContent + ' · NonbiriAPI';
      themeButton.setAttribute('aria-pressed', String(root.dataset.theme === 'dark'));
      document.querySelector('.browser-actions').setAttribute('aria-label', root.lang === 'zh' ? '下一步' : 'Next steps');
    };
    languageButton.addEventListener('click', () => {
      root.lang = root.lang === 'zh' ? 'en' : 'zh';
      save('nb.lang', root.lang);
      sync();
    });
    themeButton.addEventListener('click', () => {
      root.dataset.theme = root.dataset.theme === 'dark' ? 'light' : 'dark';
      save('nb.theme', root.dataset.theme);
      sync();
    });
    sync();
    document.querySelector('.browser-controls').hidden = false;
  });
})();
