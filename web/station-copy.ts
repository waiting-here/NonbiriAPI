import adminEn from './src/admin/i18n/en.json' with { type: 'json' };
import adminZh from './src/admin/i18n/zh.json' with { type: 'json' };
import userEn from './src/user/i18n/en.json' with { type: 'json' };
import userZh from './src/user/i18n/zh.json' with { type: 'json' };
import type { Plugin } from 'vite';

const user = { en: userEn, zh: userZh };
const admin = { en: adminEn, zh: adminZh };
type Language = keyof typeof user;
type Feature = 'privacy' | 'terms' | 'management-snapshot';
const coreModule = /^virtual:station-core\/(user|admin)\/(en|zh)$/;
const featureModule = /^virtual:feature-copy\/(privacy|terms|management-snapshot)\/(en|zh)$/;
const devPath = /^\/@station-copy\/(privacy|terms|management-snapshot)\/(en|zh)\.json$/;

function core(station: string, language: Language) {
  if (station === 'admin') {
    const catalog = admin[language];
    const history: Partial<typeof catalog.admin.managementHistory> = {
      ...catalog.admin.managementHistory,
    };
    delete history.snapshot;
    return { ...catalog, admin: { ...catalog.admin, managementHistory: history } };
  }
  const catalog = user[language];
  const { privacy, terms, ...legal } = catalog.user.legal;
  return {
    ...catalog,
    user: {
      ...catalog.user,
      legal: { ...legal, privacy: { nav: privacy.nav }, terms: { nav: terms.nav } },
    },
  };
}

function feature(scope: Feature, language: Language) {
  return scope === 'management-snapshot'
    ? admin[language].admin.managementHistory.snapshot
    : user[language].user.legal[scope];
}

// The checked-in bilingual dictionaries remain the source for builds, tests and the copy audit.
export function stationCopy(): Plugin {
  let building = false;
  return {
    name: 'station-feature-copy',
    configResolved(config) {
      building = config.command === 'build';
    },
    resolveId(id) {
      if (coreModule.test(id) || featureModule.test(id)) return '\0' + id;
    },
    load(id) {
      if (!id.startsWith('\0virtual:')) return;
      const name = id.slice(1);
      const base = coreModule.exec(name);
      if (base) return 'export default ' + JSON.stringify(core(base[1]!, base[2] as Language));
      const part = featureModule.exec(name);
      if (!part) return;
      const scope = part[1] as Feature;
      const language = part[2] as Language;
      if (!building)
        return 'export default ' + JSON.stringify(`/@station-copy/${scope}/${language}.json`);
      const ref = this.emitFile({
        type: 'asset',
        name: `station-copy-${scope}-${language}.json`,
        source: JSON.stringify(feature(scope, language)),
      });
      return 'export default import.meta.ROLLUP_FILE_URL_' + ref;
    },
    configureServer(server) {
      server.middlewares.use((request, response, next) => {
        const match = devPath.exec((request.url ?? '').split('?')[0]!);
        if (
          !match ||
          (server.config.mode === 'user' && match[1] === 'management-snapshot') ||
          (server.config.mode === 'admin' && match[1] !== 'management-snapshot')
        )
          return next();
        response.setHeader('Content-Type', 'application/json; charset=utf-8');
        response.end(JSON.stringify(feature(match[1] as Feature, match[2] as Language)));
      });
    },
  };
}
