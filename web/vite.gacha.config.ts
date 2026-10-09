import { fileURLToPath, URL } from 'node:url';
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react-swc';
import { cpSync, mkdirSync } from 'node:fs';

export default defineConfig({
  root: fileURLToPath(new URL('./src/gacha-preview', import.meta.url)),
  publicDir: fileURLToPath(new URL('./public', import.meta.url)),
  plugins: [
    react(),
    {
      name: 'gacha-preview-assets',
      apply: 'build',
      closeBundle() {
        const destination = fileURLToPath(
          new URL('./dist/gacha-preview/assets/gacha', import.meta.url),
        );
        mkdirSync(destination, { recursive: true });
        cpSync(fileURLToPath(new URL('./public/assets/gacha', import.meta.url)), destination, {
          recursive: true,
        });
      },
    },
  ],
  resolve: { alias: { '@shared': fileURLToPath(new URL('./src/shared', import.meta.url)) } },
  server: { host: '127.0.0.1', port: 5187, strictPort: true },
  preview: { host: '127.0.0.1', port: 4187, strictPort: true },
  build: {
    copyPublicDir: false,
    outDir: fileURLToPath(new URL('./dist/gacha-preview', import.meta.url)),
    emptyOutDir: true,
  },
});
