import { fileURLToPath, URL } from 'node:url';
import { defineConfig, type ConfigEnv, type UserConfig } from 'vite';
import react from '@vitejs/plugin-react-swc';

/**
 * Two independent stations, each a separate Vite build producing its own
 * bundle under web/dist/<station>. The station is selected via the Vite mode:
 * `vite build --mode admin` and `vite build --mode user`.
 */
interface StationConfig {
  root: string;
  distDir: string;
  devPort: number;
  previewPort: number;
}

const STATIONS: Record<'admin' | 'user', StationConfig> = {
  admin: { root: 'src/admin', distDir: 'admin', devPort: 5173, previewPort: 4173 },
  user: { root: 'src/user', distDir: 'user', devPort: 5174, previewPort: 4174 },
};

function stationFor(mode: string): StationConfig {
  if (mode === 'admin' || mode === 'user') {
    return STATIONS[mode];
  }
  throw new Error(
    `Unknown Vite mode "${mode}" — pass --mode admin or --mode user (scripts: dev:admin, dev:user, build, preview:admin, preview:user)`,
  );
}

export default defineConfig(({ mode }: ConfigEnv): UserConfig => {
  const station = stationFor(mode);
  return {
    root: station.root,
    plugins: [react()],
    resolve: {
      alias: {
        '@shared': fileURLToPath(new URL('./src/shared', import.meta.url)),
      },
    },
    server: {
      port: station.devPort,
      strictPort: true,
    },
    preview: {
      port: station.previewPort,
      strictPort: true,
    },
    build: {
      // Absolute path so the output lands in web/dist/<station> regardless of
      // the per-station root. Both station builds share web/dist/.
      outDir: fileURLToPath(new URL(`./dist/${station.distDir}`, import.meta.url)),
      emptyOutDir: true,
      sourcemap: false,
    },
  };
});
