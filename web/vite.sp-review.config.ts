import { fileURLToPath, URL } from 'node:url';
import { cpSync, mkdirSync } from 'node:fs';
import { dirname } from 'node:path';
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react-swc';

export default defineConfig({
  root: fileURLToPath(new URL('./src/sp-review-preview', import.meta.url)),
  publicDir: fileURLToPath(new URL('./public', import.meta.url)),
  plugins: [
    react(),
    {
      name: 'sp-review-assets',
      apply: 'build',
      closeBundle() {
        const copy = (path: string) => {
          const source = fileURLToPath(new URL('./public/assets/gacha/' + path, import.meta.url));
          const destination = fileURLToPath(
            new URL('./dist/sp-review/assets/gacha/' + path, import.meta.url),
          );
          mkdirSync(dirname(destination), { recursive: true });
          cpSync(source, destination, { recursive: true });
        };
        for (const prefix of ['', 'hires/']) {
          for (const id of [
            'l-chatgpt',
            'm-deepseek',
            'n-gemini',
            'o-claude',
            'p-claude',
            'q-glm',
          ]) {
            copy(prefix + 'cards/' + id + '.webp');
            copy(prefix + 'motion/' + id);
            if (!prefix) copy('thumbs/' + id + '.webp');
          }
          for (const id of ['l', 'm', 'n', 'o', 'p', 'q'])
            for (const type of ['covers', 'groups']) copy(prefix + type + '/' + id + '.webp');
          copy(prefix + 'appearances');
          copy(prefix + 'tomorrow');
        }
      },
    },
  ],
  resolve: { alias: { '@shared': fileURLToPath(new URL('./src/shared', import.meta.url)) } },
  server: { host: '127.0.0.1', port: 5190, strictPort: true },
  preview: { host: '127.0.0.1', port: 4190, strictPort: true },
  build: {
    copyPublicDir: false,
    outDir: fileURLToPath(new URL('./dist/sp-review', import.meta.url)),
    emptyOutDir: true,
  },
});
