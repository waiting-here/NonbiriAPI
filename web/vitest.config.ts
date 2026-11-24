import { lakeNotesDefine } from './lake-notes-copy.ts';
import { fileURLToPath, URL } from 'node:url';
import react from '@vitejs/plugin-react-swc';
import { defineConfig } from 'vitest/config';
import { stationCopy } from './station-copy.ts';

export default defineConfig({
  define: lakeNotesDefine,
  plugins: [stationCopy(), react()],
  resolve: {
    alias: {
      '@shared': fileURLToPath(new URL('./src/shared', import.meta.url)),
    },
  },
  test: {
    environment: 'jsdom',
    environmentOptions: {
      jsdom: { url: 'http://user.test/' },
    },
    setupFiles: ['./test/unit/setup.ts'],
    include: ['src/**/*.{test,spec}.{ts,tsx}', 'test/unit/**/*.{test,spec}.{ts,tsx}'],
    clearMocks: true,
    restoreMocks: true,
    unstubGlobals: true,
    testTimeout: 5_000,
    hookTimeout: 5_000,
  },
});
